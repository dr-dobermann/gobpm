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

// keyAdder is the lazy-key seam the hub reaches a waiter through (SRD-017
// §4.5). It is not part of EventWaiter, so every test that exercises a key
// learned after construction asserts its presence first.
type keyAdder interface {
	AddKey(key string) error
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

	// …and the second iteration parks afterwards, answering to "b"
	second := newKeyedProcessor("iter-1", "b")
	require.NoError(t, w.AddEventProcessor(second))

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

	ka, ok := w.(keyAdder)
	require.True(t, ok, "a message waiter must accept a lazy key")

	require.NoError(t, ka.AddKey("late"),
		"a key learned before the subscription exists is accepted")

	// "late" is reachable ONLY if AddKey buffered it: it belongs to no
	// processor, so Service cannot rediscover it from the processor list.

	require.NoError(t, w.Service(ctx))

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

	ka, ok := w.(keyAdder)
	require.True(t, ok, "a message waiter must accept a lazy key")

	require.NoError(t, ka.AddKey("later"))

	require.NoError(t, rt.MessageBroker().Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "ORD-X", CorrelationKey: "later"}))

	select {
	case <-proc.got:
	case <-time.After(2 * time.Second):
		t.Fatal("a key added to a live subscription never reached the broker")
	}
}

// TestJoiningProcessorWithARefusedKeyDoesNotHalfJoin pins the failure half of
// the join: the subscription refuses the joining processor's key.
//
// The processor must NOT be in the list afterwards. If it were, a retry would
// find it via the duplicate check, return nil, and report success while the
// iteration stayed unreachable by its own key — the #320 symptom, restored by
// the very code meant to prevent it.
//
// The real broker cannot produce this: membroker's AddKey never fails. It
// takes a broker built to refuse.
func TestJoiningProcessorWithARefusedKeyDoesNotHalfJoin(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	broker := messagingtest.NewFailingBroker()
	rt := brokerRT{EngineRuntime: enginert.Default(), broker: broker}

	first := newKeyedProcessor("iter-0", "a")

	w, err := waiters.NewMessageWaiter(hub, first, eDef, "", rt)
	require.NoError(t, err)

	// Service subscribes with "a" and adds nothing afterwards, so the
	// refusal cannot fire here — it fires on the join below.
	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { _ = w.Stop() })

	second := newKeyedProcessor("iter-1", "b")

	err = w.AddEventProcessor(second)
	require.Error(t, err, "a key the subscription refuses must fail the join")
	require.ErrorIs(t, err, messagingtest.ErrInjected)

	require.NotContains(t, w.EventProcessors(), eventproc.EventProcessor(second),
		"a processor whose key was refused must not be left half-joined: a "+
			"retry would short-circuit on the duplicate check and report "+
			"success with the key still missing")

	subs := broker.Subscriptions()
	require.Len(t, subs, 1)
	require.Equal(t, []string{"a"}, subs[0].Keys,
		"the subscription was created from the first processor's keys")
	require.Empty(t, subs[0].Added(),
		"the refused key must not be recorded as applied")
}

// TestKeyRefusedDuringSubscribeFailsTheWaiter covers the last unreachable
// path: a key learned WHILE Subscribe was running, which the waiter buffers
// and applies the moment the subscription appears — and which the broker then
// refuses.
//
// The waiter must end WSFailed. It used to return the error with mw.sub
// already set and its state left WSReady: a retry would build a second
// subscription, leak the first, and drop the buffered keys permanently.
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
		ka, ok := w.(keyAdder)
		require.True(t, ok, "a message waiter must accept a lazy key")
		require.NoError(t, ka.AddKey("late"),
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
}
