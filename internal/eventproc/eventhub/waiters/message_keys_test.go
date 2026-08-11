package waiters_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

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

	ka, ok := w.(interface{ AddKey(string) error })
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
