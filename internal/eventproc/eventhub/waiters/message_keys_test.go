package waiters_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/messagingtest"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// keyed asserts the waiter carries the optional correlation-key seam and
// returns it. The seam is eventproc.KeyedWaiter — declared rather than asserted
// anonymously (FIX-041 §3.1 D4) — and it is optional, so every test that
// exercises a key learned after construction asserts its presence first.
func keyed(t *testing.T, w eventproc.EventWaiter) eventproc.KeyedWaiter {
	t.Helper()

	kw, ok := w.(eventproc.KeyedWaiter)
	require.True(t, ok, "a message waiter must be a KeyedWaiter")

	return kw
}

// join performs both halves of a join the way the EventHub does: the registry
// half, then the broker half once the hub's lock would have been released
// (FIX-041 §3.1 D1). Tests in this file drive the waiter directly, so they must
// drive both halves or they are testing a join no caller performs.
func join(
	t *testing.T, w eventproc.EventWaiter, ep eventproc.EventProcessor,
) error {
	t.Helper()

	if err := w.AddEventProcessor(ep); err != nil {
		return err
	}

	return keyed(t, w).ApplyProcessorKeys(ep)
}

// keyedProcessor is an EventProcessor that answers to correlation keys — the
// shape a parked Multi-Instance iteration presents to the waiter. The mock
// generated for EventProcessor cannot carry CorrelationKeys, which is the
// method the subscription is built from, so the fixture is written by hand.
type keyedProcessor struct {
	id   string
	keys []string
	got  chan flow.EventDefinition
}

func newKeyedProcessor(id string, keys ...string) *keyedProcessor {
	return &keyedProcessor{
		id:   id,
		keys: keys,
		got:  make(chan flow.EventDefinition, 1),
	}
}

func (p *keyedProcessor) ID() string { return p.id }

func (p *keyedProcessor) CorrelationKeys() []string { return p.keys }

func (p *keyedProcessor) ProcessEvent(
	_ context.Context, ed flow.EventDefinition,
) error {
	select {
	case p.got <- ed:
	default:
	}

	return nil
}

var _ eventproc.KeyedProcessor = (*keyedProcessor)(nil)

// TestJoiningProcessorBringsItsKey is #320's regression pin.
//
// The subscription is built from the keys known when Service subscribes. A
// processor that JOINS afterwards — the second iteration of a Multi-Instance
// activity parking at the same catch — used to add itself to the processor
// list and nothing else, so the broker still routed only the first
// iteration's key. Its envelope then matched no subscription and waited in
// the broker's inbox forever: the test hung at its deadline with no data
// race, at any deadline, because nothing was ever going to arrive.
//
// The lazy AddEventKey path cannot cover this — it silently no-ops while the
// waiter is not yet in the hub's map, which is exactly the window in which a
// sibling iteration derives its key.
func TestJoiningProcessorBringsItsKey(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)
	rt := enginert.Default()

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	first := newKeyedProcessor("iter-0", "a")

	w, err := waiters.NewMessageWaiter(hub, first, eDef, "", rt)
	require.NoError(t, err)

	// the waiter subscribes knowing only "a"
	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { _ = w.Stop() })

	// …and the second iteration parks afterwards, answering to "b"
	second := newKeyedProcessor("iter-1", "b")
	require.NoError(t, join(t, w, second))

	require.NoError(t, rt.MessageBroker().Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "ORD-B", CorrelationKey: "b"}))

	select {
	case <-second.got:
	case <-time.After(2 * time.Second):
		t.Fatal("the envelope keyed for the joining iteration never " +
			"arrived: its key is missing from the subscription (#320)")
	}
}

// TestKeyAddedBeforeSubscribeSurvives pins the other half. AddKey used to
// return nil when the subscription did not exist yet, on the reasoning that
// the key would be read from the processors at Service time — true only for a
// key the processor already answers to. A key learned by the instance in
// between was dropped with no error and no log.
func TestKeyAddedBeforeSubscribeSurvives(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)
	rt := enginert.Default()

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	// The processor answers to a key of its own, so the subscription is
	// KEYED rather than a wildcard. A keyless processor leaves a wildcard
	// subscription that matches every envelope, which would let this test
	// pass with the key dropped — it did, before this comment existed.
	proc := newKeyedProcessor("iter-0", "own")

	w, err := waiters.NewMessageWaiter(hub, proc, eDef, "", rt)
	require.NoError(t, err)

	require.NoError(t, keyed(t, w).AddKey("late"),
		"a key learned before the subscription exists is accepted")

	// "late" is reachable ONLY if AddKey buffered it: it belongs to no
	// processor, so Service cannot rediscover it from the processor list.

	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { _ = w.Stop() })

	require.NoError(t, rt.MessageBroker().Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "ORD-L", CorrelationKey: "late"}))

	select {
	case <-proc.got:
	case <-time.After(2 * time.Second):
		t.Fatal("a key added before Service was dropped rather than " +
			"applied when the subscription appeared (#320)")
	}
}

// TestLazyKeyReachesALiveSubscription covers the ordinary half of AddKey: the
// subscription already exists, so the key goes straight to the broker instead
// of being buffered. It is the path an iteration takes when it derives its key
// after its waiter is serviced — the common case, and the one whose failure
// the two tests above were written for.
func TestLazyKeyReachesALiveSubscription(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)
	rt := enginert.Default()

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	// keyed, so the subscription routes by key rather than matching
	// everything — a wildcard would let this pass with the key dropped.
	proc := newKeyedProcessor("iter-0", "own")

	w, err := waiters.NewMessageWaiter(hub, proc, eDef, "", rt)
	require.NoError(t, err)

	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { _ = w.Stop() })

	require.NoError(t, keyed(t, w).AddKey("later"))

	require.NoError(t, rt.MessageBroker().Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "ORD-X", CorrelationKey: "later"}))

	select {
	case <-proc.got:
	case <-time.After(2 * time.Second):
		t.Fatal("a key added to a live subscription never reached the broker")
	}
}

// TestJoinHalvesAreSeparable pins the split D1 rests on: AddEventProcessor is
// registry work that reaches nothing outside the waiter, and ApplyProcessorKeys
// is the only half that talks to the broker.
//
// This is what lets the EventHub hold its one lock across the first half and
// not the second. While the two were one method, a join reached the host's
// broker from inside eh.m and every registration, unregistration and lookup in
// the engine queued behind it (FIX-038 §1.1, reintroduced by the #320 fix).
// The hub-side timing consequence is pinned by TestJoinDoesNotHoldTheHubLock;
// this test pins the property that makes it possible, which is cheaper to run
// and more precise about which half is at fault when it breaks.
func TestJoinHalvesAreSeparable(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	var reached []string

	broker := &messagingtest.FailingBroker{
		// Set before Service: a subscription copies the hook when it is
		// handed out.
		OnAddKey: func(key string) { reached = append(reached, key) },
	}
	rt := brokerRT{EngineRuntime: enginert.Default(), broker: broker}

	w, err := waiters.NewMessageWaiter(hub, newKeyedProcessor("iter-0", "a"),
		eDef, "", rt)
	require.NoError(t, err)

	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { _ = w.Stop() })

	second := newKeyedProcessor("iter-1", "b")

	require.NoError(t, w.AddEventProcessor(second))
	require.Contains(t, w.EventProcessors(), eventproc.EventProcessor(second),
		"the registry half registers")
	require.Empty(t, reached,
		"the registry half must reach no broker: it runs under the hub's "+
			"engine-wide lock, and a host call there stalls the whole engine")

	require.NoError(t, keyed(t, w).ApplyProcessorKeys(second))
	require.Equal(t, []string{"b"}, reached,
		"the foreign half applies the joining processor's key")
	require.Equal(t, []string{"b"}, broker.Subscriptions()[0].Added())
}

// TestPartialKeyFailureDiscardsTheSubscription pins D2: a key the broker
// refuses costs the waiter its whole subscription.
//
// The processor carries TWO keys and the broker accepts one before refusing, so
// the subscription really is left partly keyed — the state the port cannot
// repair, because messaging.Subscription can grow a key-set but not shrink one.
// Leaving it would leave an orphan key on a live subscription, quietly eating
// every message addressed to it: #320's failure mode made permanent.
//
// Asserting the returned error is not enough, and that is the point of this
// test: the pre-fix code returned exactly this error while keeping the
// subscription (FIX-041 §1.3, §4.3).
func TestPartialKeyFailureDiscardsTheSubscription(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	broker := &messagingtest.FailingBroker{
		AddKeyErr:   messagingtest.ErrInjected,
		AddKeyAfter: 1, // the first key lands, the second is refused
	}
	rt := brokerRT{EngineRuntime: enginert.Default(), broker: broker}

	w, err := waiters.NewMessageWaiter(hub, newKeyedProcessor("iter-0", "a"),
		eDef, "", rt)
	require.NoError(t, err)

	// Service subscribes with "a" as a creation key and adds nothing
	// afterwards, so the refusal cannot fire here — it fires on the join.
	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { _ = w.Stop() })

	second := newKeyedProcessor("iter-1", "b", "c")

	err = join(t, w, second)
	require.Error(t, err, "a key the subscription refuses must fail the join")
	require.ErrorIs(t, err, messagingtest.ErrInjected)

	subs := broker.Subscriptions()
	require.Len(t, subs, 1)
	require.Equal(t, []string{"a"}, subs[0].Keys,
		"the subscription was created from the first processor's keys")
	require.Equal(t, []string{"b"}, subs[0].Added(),
		"exactly one key landed before the refusal — the partial state that "+
			"cannot be repaired in place")
	require.True(t, subs[0].Unsubscribed(),
		"a key-set that cannot be repaired is discarded whole: the "+
			"subscription must be gone, not left carrying the orphan key")

	// Wait for the service goroutine before reading the state. Dropping the
	// subscription closes the channel it is selecting on, so it wakes and
	// records an exit state of its own — and a waiter that broke must not end
	// up labelled Stopped, which reads as an orderly shutdown. Waiting also
	// pins that the goroutine exits at all rather than spinning on a closed
	// channel.
	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the service goroutine of a failed waiter never exited")
	}

	require.Equal(t, eventproc.WSFailed, w.State(),
		"a waiter that gave up its subscription stays failed: the service "+
			"goroutine woken by its own closed channel must not relabel it "+
			"Stopped, which reports an orderly shutdown for a break")
}

// TestKeyRefusedDuringSubscribeFailsTheWaiter covers the last unreachable
// path: a key learned WHILE Subscribe was running, which the waiter buffers
// and applies the moment the subscription appears — and which the broker then
// refuses.
//
// The waiter must end WSFailed with its subscription dropped. It used to
// return the error with mw.sub already set and its state left WSReady: a retry
// would build a second subscription, leak the first, and drop the buffered
// keys permanently.
func TestKeyRefusedDuringSubscribeFailsTheWaiter(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	var w eventproc.EventWaiter

	broker := messagingtest.NewFailingBroker()
	broker.OnSubscribe = func() {
		// Inside Subscribe: the subscription does not exist yet, so the key
		// is buffered rather than applied. This is the window a Multi-
		// Instance iteration derives its key in while a sibling registers
		// (#320) — unreachable from outside, since Subscribe returning is
		// what closes it.
		require.NoError(t, keyed(t, w).AddKey("late"),
			"a key learned during Subscribe is buffered, not refused here")
	}

	rt := brokerRT{EngineRuntime: enginert.Default(), broker: broker}

	w, err := waiters.NewMessageWaiter(hub, newKeyedProcessor("iter-0", "own"),
		eDef, "", rt)
	require.NoError(t, err)

	err = w.Service(ctx)
	require.Error(t, err, "a buffered key the broker refuses must fail Service")
	require.ErrorIs(t, err, messagingtest.ErrInjected)

	require.Equal(t, eventproc.WSFailed, w.State(),
		"a waiter that could not apply its keys is failed, not ready: a "+
			"retry would leak the subscription it already holds")

	subs := broker.Subscriptions()
	require.Len(t, subs, 1)
	require.True(t, subs[0].Unsubscribed(),
		"the subscription the waiter could not finish keying must be given "+
			"back, not left running behind a failed waiter")
}

// TestJoinBeforeServiceSubscribesEachKeyOnce pins the de-duplication (D3).
//
// A processor that joins before Service has its keys in TWO places — buffered
// by ApplyProcessorKeys, and readable from the processor itself when Service
// snapshots the list — so Subscribe used to receive each of them twice.
// membroker hides this (its key-set is a map), but the port is
// host-implementable and an adapter that turns each key into a queue-level
// registration would make two of them.
//
// The buffering is NOT the bug and must not be removed to fix this: a processor
// joining after Service snapshots its list and before it publishes the
// subscription is in neither, and its key would vanish exactly as in #320.
func TestJoinBeforeServiceSubscribesEachKeyOnce(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	broker := &messagingtest.FailingBroker{}
	rt := brokerRT{EngineRuntime: enginert.Default(), broker: broker}

	w, err := waiters.NewMessageWaiter(hub, newKeyedProcessor("iter-0", "a"),
		eDef, "", rt)
	require.NoError(t, err)

	// joins while there is no subscription yet, so its key is buffered
	require.NoError(t, join(t, w, newKeyedProcessor("iter-1", "b")))

	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { _ = w.Stop() })

	subs := broker.Subscriptions()
	require.Len(t, subs, 1)
	require.ElementsMatch(t, []string{"b", "a"}, subs[0].Keys,
		"each key reaches Subscribe exactly once, however many places the "+
			"waiter learned it from")
}
