package eventhub_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/messagingtest"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// hubBrokerRT swaps the message broker of an embedded EngineRuntime so a hub
// test can hand the engine a broker that refuses keys or blocks inside them.
type hubBrokerRT struct {
	renv.EngineRuntime

	broker messaging.MessageBroker
}

func (b hubBrokerRT) MessageBroker() messaging.MessageBroker { return b.broker }

// hubKeyedProcessor is an EventProcessor answering to correlation keys — a
// parked Multi-Instance iteration. The generated EventProcessor mock cannot
// carry CorrelationKeys, which is the method the subscription is built from.
type hubKeyedProcessor struct {
	id   string
	keys []string
}

func (p *hubKeyedProcessor) ID() string { return p.id }

func (p *hubKeyedProcessor) CorrelationKeys() []string { return p.keys }

func (p *hubKeyedProcessor) ProcessEvent(
	_ context.Context, _ flow.EventDefinition,
) error {
	return nil
}

var _ eventproc.KeyedProcessor = (*hubKeyedProcessor)(nil)

// startedHub builds a hub over broker and starts it.
func startedHub(t *testing.T, broker messaging.MessageBroker) *eventhub.EventHub {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	hub, err := eventhub.New(
		hubBrokerRT{EngineRuntime: enginert.Default(), broker: broker})
	require.NoError(t, err)
	require.NoError(t, hub.Start(t.Context()))

	return hub
}

// TestJoinDoesNotHoldTheHubLock is FIX-041 §1.1's regression pin, and the only
// test here that asserts a TIMING property rather than a value.
//
// A join has two halves: adding the processor to the waiter's list (registry
// work, correctly under the hub's one lock) and handing that processor's
// correlation keys to the waiter's broker subscription (a call into the HOST,
// which may be a remote queue). While the second half ran under eh.m, every
// registration, unregistration and lookup in the whole engine queued behind one
// host call — FIX-038 §1.1's defect, reintroduced by the #320 fix and invisible
// to it because holding a lock is not a wrong answer, only a slow one.
//
// It takes a broker that can be made to hold still: the real one returns far
// too fast to observe, and sleeping instead would only make the assertion
// flaky. The engine is pinned inside AddKey, and an unrelated hub lookup must
// still complete.
func TestJoinDoesNotHoldTheHubLock(t *testing.T) {
	var (
		entered = make(chan struct{})
		release = make(chan struct{})
	)

	broker := &messagingtest.FailingBroker{
		OnAddKey: func(_ string) {
			close(entered)
			<-release
		},
	}

	hub := startedHub(t, broker)
	eDef := msgEDef(t, "order placed")

	// The first registration builds and services the waiter; its key is a
	// creation parameter of Subscribe, so it never reaches AddKey.
	require.NoError(t,
		hub.RegisterEvent(&hubKeyedProcessor{id: "iter-0", keys: []string{"a"}}, eDef))

	joined := make(chan error, 1)

	go func() {
		joined <- hub.RegisterEvent(
			&hubKeyedProcessor{id: "iter-1", keys: []string{"b"}}, eDef)
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the joining registration never reached the broker")
	}

	// The engine is now inside a host call it cannot hurry. An unrelated
	// lookup — this one misses, so it touches nothing but the registry — must
	// not be queued behind it.
	looked := make(chan error, 1)

	go func() { looked <- hub.AddEventKey("no-such-definition", "K1") }()

	select {
	case err := <-looked:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("a hub lookup blocked while a join was inside the broker: " +
			"the engine is holding eh.m across a host call (FIX-038 §1.1)")
	}

	close(release)
	require.NoError(t, <-joined)

	require.NoError(t, hub.Shutdown(t.Context()))
}

// TestJoinRegistersBeforeItReachesTheBroker pins the ORDER of a join's two
// halves, which is what makes the split safe rather than merely lock-free.
//
// Register-then-apply leaves a window in which the processor is listed and its
// key is not yet subscribed, and that window drops nothing: the broker routes
// no envelope for a key it has not been given, and an unmatched envelope waits
// in its inbox until a subscription wants it (ADR-006 v.5 §2.4). The inverse
// order leaves the opposite window — the key IS routed while the processor is
// not listed — so a matching envelope is consumed and thrown away. It also
// hands a concurrent UnregisterEvent a waiter with no sign of the registration
// attaching to it, which is the stranding FIX-038 §1.3 fixed.
//
// The order is only visible from INSIDE the broker call: by the time
// RegisterEvent returns, both halves are done. The probe is an unregister of
// the joining processor, which can only succeed if it is already listed.
func TestJoinRegistersBeforeItReachesTheBroker(t *testing.T) {
	var (
		second   = &hubKeyedProcessor{id: "iter-1", keys: []string{"b"}}
		hub      *eventhub.EventHub
		eDef     = msgEDef(t, "order placed")
		probeErr = errors.New("the hook never ran")
	)

	broker := &messagingtest.FailingBroker{
		OnAddKey: func(_ string) { probeErr = hub.UnregisterEvent(second, eDef.ID()) },
	}

	hub = startedHub(t, broker)

	require.NoError(t,
		hub.RegisterEvent(&hubKeyedProcessor{id: "iter-0", keys: []string{"a"}}, eDef))

	require.NoError(t, hub.RegisterEvent(second, eDef))

	require.NoError(t, probeErr,
		"the joining processor must be registered BEFORE its key reaches the "+
			"broker: in the reverse order the key routes envelopes to a "+
			"processor the waiter does not yet list, and each one is consumed "+
			"and discarded")

	require.NoError(t, hub.Shutdown(t.Context()))
}

// TestRefusedJoinKeyLeavesNoWaiterBehind pins what the hub does with the
// wreckage of a refused key (FIX-041 §3.1 D2).
//
// The waiter discards its whole subscription, because the port can grow a
// key-set but not shrink one and a partly-applied set cannot be repaired in
// place. That leaves it DEAD — and a dead waiter left in the registry is worse
// than the bug it came from: every later registration for the same definition
// joins it and receives nothing, forever, with no error anywhere. So the hub
// unmaps it, and the next registration builds a fresh one.
func TestRefusedJoinKeyLeavesNoWaiterBehind(t *testing.T) {
	broker := messagingtest.NewFailingBroker() // every AddKey refused
	hub := startedHub(t, broker)
	eDef := msgEDef(t, "order placed")

	first := &hubKeyedProcessor{id: "iter-0", keys: []string{"a"}}
	require.NoError(t, hub.RegisterEvent(first, eDef))

	second := &hubKeyedProcessor{id: "iter-1", keys: []string{"b"}}

	err := hub.RegisterEvent(second, eDef)
	require.Error(t, err, "a refused correlation key must fail the registration")
	require.ErrorIs(t, err, messagingtest.ErrInjected)

	// Not half-joined: were the processor left listed, a retry would find it
	// on the duplicate check, return nil and report success while the
	// iteration stayed unreachable by its own key — #320, restored by the code
	// meant to prevent it.
	require.Error(t, hub.UnregisterEvent(second, eDef.ID()),
		"the processor whose key was refused must not be left registered")

	// And the waiter it failed is gone, not sitting dead in the registry.
	require.Error(t, hub.UnregisterEvent(first, eDef.ID()),
		"a waiter that gave up its subscription must be unmapped: left in "+
			"place it is joined by every later registration and delivers to none")

	require.NoError(t, hub.RegisterEvent(first, eDef),
		"the next registration must build a fresh waiter")

	require.Len(t, broker.Subscriptions(), 2,
		"the fresh waiter subscribes anew rather than inheriting the "+
			"subscription the failed one gave back")

	require.NoError(t, hub.Shutdown(t.Context()))
}
