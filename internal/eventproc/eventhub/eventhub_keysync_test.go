package eventhub_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// keyedProcessor is an EventProcessor carrying declared correlation keys — the
// shape the message waiter reads structurally to build its broker subscription
// (SRD-017 §4.3). Its key-set is mutable under a lock, because the window these
// tests drive is exactly the one where a key is declared while a registration
// for the same definition is in flight.
type keyedProcessor struct {
	got  chan flow.EventDefinition
	id   string
	keys []string
	m    sync.Mutex
}

func newKeyedProcessor(id string, keys ...string) *keyedProcessor {
	return &keyedProcessor{
		id:   id,
		keys: keys,
		got:  make(chan flow.EventDefinition, 4),
	}
}

func (p *keyedProcessor) ID() string { return p.id }

func (p *keyedProcessor) CorrelationKeys() []string {
	p.m.Lock()
	defer p.m.Unlock()

	return append([]string(nil), p.keys...)
}

func (p *keyedProcessor) declare(key string) {
	p.m.Lock()
	defer p.m.Unlock()

	p.keys = append(p.keys, key)
}

func (p *keyedProcessor) ProcessEvent(
	_ context.Context, eDef flow.EventDefinition,
) error {
	p.got <- eDef

	return nil
}

// awaitDelivery fails unless an event reaches the processor.
func (p *keyedProcessor) awaitDelivery(t *testing.T, why string) {
	t.Helper()

	select {
	case <-p.got:
	case <-time.After(2 * time.Second):
		t.Fatal(why)
	}
}

// brokerRuntime overrides only the message broker of the default runtime.
type brokerRuntime struct {
	renv.EngineRuntime
	broker messaging.MessageBroker
}

func (r brokerRuntime) MessageBroker() messaging.MessageBroker { return r.broker }

// latchBroker holds the FIRST Subscribe open until the test releases it, which
// turns the install window — the gap between a waiter subscribing and the hub
// installing it in its registry — into a controlled interval instead of a race
// to be hit by repetition.
type latchBroker struct {
	messaging.MessageBroker

	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newLatchBroker() *latchBroker {
	return &latchBroker{
		MessageBroker: membroker.New(),
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
	}
}

func (b *latchBroker) Subscribe(
	ctx context.Context, name string, keys ...string,
) (messaging.Subscription, error) {
	b.once.Do(func() {
		close(b.entered)
		<-b.release
	})

	return b.MessageBroker.Subscribe(ctx, name, keys...)
}

// startedHub returns a started hub over rt, torn down with the test.
func startedHub(t *testing.T, rt renv.EngineRuntime) *eventhub.EventHub {
	t.Helper()

	hub, err := eventhub.New(rt)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, hub.Start(ctx))

	return hub
}

// TestKeyDeclaredDuringInstallReachesSubscription is SRD-090.A T-12. A waiter
// subscribes the broker BEFORE the hub installs it in its registry, and
// AddEventKey — the lazy-association path — cannot find an uninstalled waiter,
// so a key declared inside that window used to reach neither: the subscription
// stayed keyed to what the processor had declared when Subscribe read it, and
// every message carrying the new key was buffered by the broker unrouted,
// forever. Two parallel Multi-Instance iterations parking at one shared message
// catch hit it whenever the second declares its iteration key while the first
// is still subscribing (measured: 1 run in 3 of the pkg/thresher pair).
//
// The window is a latch here, not a sleep, so the test does not depend on
// losing a race.
func TestKeyDeclaredDuringInstallReachesSubscription(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	broker := newLatchBroker()
	hub := startedHub(t, brokerRuntime{
		EngineRuntime: enginert.Default(), broker: broker})

	eDef := msgEDef(t, "confirm")
	p := newKeyedProcessor("p", "a")

	registered := make(chan error, 1)

	go func() { registered <- hub.RegisterEvent(p, eDef) }()

	<-broker.entered // the waiter is subscribing; nothing is installed yet

	// the sibling iteration declares its own key and asks the hub to extend the
	// receivers — which finds no waiter and no-ops, benignly.
	p.declare("b")
	require.NoError(t, hub.AddEventKey(eDef.ID(), "b"))

	close(broker.release)
	require.NoError(t, <-registered)

	require.NoError(t, broker.Publish(context.Background(),
		messaging.Envelope{Name: "confirm", CorrelationKey: "b"}))

	p.awaitDelivery(t,
		"a key declared while the waiter was being installed left the "+
			"subscription unable to route its message")
}

// TestJoiningProcessorKeysTheSubscription is SRD-090.A T-13: a processor that
// joins an already-installed waiter contributes its declared keys too. The
// engine's held subscriptions (SRD-071) make this the ordinary case — one
// holder per instance, all joining one waiter for the shared catch definition —
// so without it the second conversation is unreachable and its instance never
// wakes.
func TestJoiningProcessorKeysTheSubscription(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rt := enginert.Default()
	hub := startedHub(t, rt)

	eDef := msgEDef(t, "confirm")

	first := newKeyedProcessor("first", "a")
	require.NoError(t, hub.RegisterEvent(first, eDef))

	second := newKeyedProcessor("second", "b")
	require.NoError(t, hub.RegisterEvent(second, eDef))

	require.NoError(t, rt.MessageBroker().Publish(context.Background(),
		messaging.Envelope{Name: "confirm", CorrelationKey: "b"}))

	// the waiter fans a matched envelope out to every processor it carries;
	// the point under test is that the envelope was routed to it at all.
	second.awaitDelivery(t,
		"the joining processor's key never reached the broker subscription")
}

// TestWildcardSubscriptionIsNotNarrowed pins the guard the key sync needs: a
// subscription created with no keys receives every message for its name, and
// keying it would cut off the keyless processor that asked for exactly that (an
// instance-starter, or a holder for an instance that established no
// conversation key). The broker offers no way back from keyed to wildcard, so
// the sync must leave it alone rather than "grow" it.
func TestWildcardSubscriptionIsNotNarrowed(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rt := enginert.Default()
	hub := startedHub(t, rt)

	eDef := msgEDef(t, "confirm")

	wild := newKeyedProcessor("wild") // declares nothing → wildcard
	require.NoError(t, hub.RegisterEvent(wild, eDef))

	keyed := newKeyedProcessor("keyed", "b")
	require.NoError(t, hub.RegisterEvent(keyed, eDef))

	require.NoError(t, rt.MessageBroker().Publish(context.Background(),
		messaging.Envelope{Name: "confirm"}))

	wild.awaitDelivery(t,
		"a joining processor's key narrowed a wildcard subscription")
}

// addKeyErrSub is a subscription whose key-set cannot grow.
type addKeyErrSub struct{ ch chan messaging.Envelope }

func (s addKeyErrSub) C() <-chan messaging.Envelope { return s.ch }
func (addKeyErrSub) AddKey(string) error            { return fmt.Errorf("broker refused") }
func (addKeyErrSub) Unsubscribe() error             { return nil }

// addKeyErrBroker hands out subscriptions that refuse to grow.
type addKeyErrBroker struct{}

func (addKeyErrBroker) Publish(context.Context, messaging.Envelope) error { return nil }

func (addKeyErrBroker) Subscribe(
	context.Context, string, ...string,
) (messaging.Subscription, error) {
	return addKeyErrSub{ch: make(chan messaging.Envelope)}, nil
}

// TestKeySyncFailureFailsRegistration covers the failing arm: a broker that
// refuses to extend a key-set leaves the receiver unreachable by that key, so
// the registration reports it instead of parking a wait no message can ever
// reach. Both points that sync are checked — installing a waiter, and joining
// one — since a joining processor's key is exactly as lost as the other's when
// the broker declines it.
func TestKeySyncFailureFailsRegistration(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("on install", func(t *testing.T) {
		hub := startedHub(t, brokerRuntime{
			EngineRuntime: enginert.Default(), broker: addKeyErrBroker{}})

		err := hub.RegisterEvent(
			newKeyedProcessor("p", "a"), msgEDef(t, "confirm"))
		require.ErrorContains(t, err, "sync the waiter's correlation keys")
	})

	t.Run("on join", func(t *testing.T) {
		hub := startedHub(t, brokerRuntime{
			EngineRuntime: enginert.Default(), broker: pickyBroker{}})

		eDef := msgEDef(t, "confirm")
		require.NoError(t,
			hub.RegisterEvent(newKeyedProcessor("first", "a"), eDef))

		err := hub.RegisterEvent(newKeyedProcessor("second", refusedKey), eDef)
		require.ErrorContains(t, err, "sync the waiter's correlation keys")
	})
}

// valueProcessor is an EventProcessor implemented on a STRUCT whose slice
// field makes the type uncomparable — a shape a host can legitimately write,
// since pkg/eventproc.EventProcessor is a public contract.
type valueProcessor struct {
	id   string
	keys []string
}

func (p valueProcessor) ID() string { return p.id }

func (valueProcessor) ProcessEvent(context.Context, flow.EventDefinition) error {
	return nil
}

// TestUncomparableProcessorRefused pins the guard at the boundary: a waiter
// identifies its processors by value, and Go PANICS rather than reporting
// false when two interface values of one uncomparable dynamic type are
// compared. Without the check the hub crashed on the SECOND registration for a
// definition, inside the waiter — so the refusal has to name the type at the
// call that can still act on it.
func TestUncomparableProcessorRefused(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	hub := startedHub(t, enginert.Default())
	eDef := msgEDef(t, "confirm")

	err := hub.RegisterEvent(valueProcessor{id: "host"}, eDef)
	require.ErrorContains(t, err, "uncomparable type")
	require.ErrorContains(t, err, "register a pointer to it instead")

	require.ErrorContains(t,
		hub.RegisterPersistentEvent(valueProcessor{id: "starter"}, eDef),
		"uncomparable type")

	// the refusal is total: nothing was installed, so the second registration
	// that used to panic never happens.
	require.NoError(t, hub.RegisterEvent(newKeyedProcessor("ok", "a"), eDef))
	require.NoError(t, hub.RegisterEvent(newKeyedProcessor("ok2", "b"), eDef))
}

// refusedKey is the one key pickyBroker's subscriptions decline.
const refusedKey = "boom"

// pickySub takes every key but refusedKey, so a test can let the first
// registration through and fail the second.
type pickySub struct{ ch chan messaging.Envelope }

func (s pickySub) C() <-chan messaging.Envelope { return s.ch }
func (pickySub) Unsubscribe() error             { return nil }

func (pickySub) AddKey(key string) error {
	if key == refusedKey {
		return fmt.Errorf("broker refused %q", key)
	}

	return nil
}

// pickyBroker hands out selectively-refusing subscriptions.
type pickyBroker struct{}

func (pickyBroker) Publish(context.Context, messaging.Envelope) error { return nil }

func (pickyBroker) Subscribe(
	context.Context, string, ...string,
) (messaging.Subscription, error) {
	return pickySub{ch: make(chan messaging.Envelope)}, nil
}
