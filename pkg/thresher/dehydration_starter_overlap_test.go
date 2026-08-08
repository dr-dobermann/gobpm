package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// FIX-027 §4.1.2 — the one point where the two subscription mechanisms observe
// the SAME event.
//
// BPMN permits a message name to be both a process's instantiating start
// trigger and an in-flight instance's wait. Two independent subscribers then
// see one published message:
//
//   - the definition-scoped instanceStarter, which resolves an already-seen
//     correlation key to "join, no duplicate" and drops it;
//   - the instance's OWN keyed holder, which must still wake it.
//
// The other message tests use two distinct names, so the starter never observes
// the instance's message and this interaction is otherwise unexercised.

// overlapProcess builds start(message M, keyed) -> catch(message M) -> lane
// -> end: ONE message name serving both the instantiating start and the
// mid-flow wait.
func overlapProcess(
	t *testing.T, key string, arrived *atomic.Int32,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	const msgName = "order event"

	p, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	msg := func() *bpmncommon.Message {
		return bpmncommon.MustMessage(msgName, data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID("order_in")))
	}

	start, err := events.NewStartEvent("start",
		events.WithMessageTrigger(
			events.MustMessageEventDefinition(msg(), nil)),
		events.WithCorrelationKey(orderKeyFor(t, msgName)),
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("await-second",
		events.MustMessageEventDefinition(msg(), nil),
		foundation.WithID(key+"-catch"))
	require.NoError(t, err)

	lane := countingLane(t, key+"-lane", arrived)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, catch, lane, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, catch)
	link(t, catch, lane)
	link(t, lane, end)

	return p
}

// countingLane records how many times the post-wait lane ran.
func countingLane(
	t *testing.T, id string, hits *atomic.Int32,
) flow.Node {
	t.Helper()

	op, err := gooper.New(id,
		func(context.Context, service.DataReader,
			*data.ItemDefinition) (*data.ItemDefinition, error) {
			hits.Add(1)

			return nil, nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(id, op,
		activities.WithoutParams(), foundation.WithID(id))
	require.NoError(t, err)

	return st
}

// TestSameMessageNameStartsAndWakes: with one message name on both sides, a
// message for an ALREADY-SEEN key must wake the dehydrated instance without
// starting a second one, and a message for an UNSEEN key must start a new
// instance without being swallowed by the wake path.
func TestSameMessageNameStartsAndWakes(t *testing.T) {
	repo := memrepo.New()
	broker := membroker.New()

	var arrived atomic.Int32

	p := overlapProcess(t, "dehy-overlap", &arrived)

	th, fw, _, cancel := bootDehydrationEngineWithBroker(t, "engine-OV",
		repo, broker, p)
	defer cancel()

	ctx := context.Background()

	// first message, key ORD-1 — unseen, so it INSTANTIATES.
	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "order event", Payload: "ORD-1", CorrelationKey: "ORD-1"}))

	require.Eventually(t, func() bool {
		return len(instanceIDs(t, th, thresher.InstanceQuery{})) == 1
	}, 3*time.Second, 10*time.Millisecond,
		"the first message starts exactly one instance")

	// it parks on the catch — a held message wait — and releases.
	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 3*time.Second, 10*time.Millisecond,
		"the instance dehydrates on its message catch")

	require.Zero(t, arrived.Load(), "the lane is past the wait, not yet run")

	// second message, SAME key — the starter must join-and-drop while the
	// instance's own holder wakes it.
	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "order event", Payload: "ORD-1", CorrelationKey: "ORD-1"}))

	require.Eventually(t, func() bool { return arrived.Load() == 1 },
		3*time.Second, 10*time.Millisecond,
		"a message for a seen key wakes the dehydrated instance")

	require.Len(t, instanceIDs(t, th, thresher.InstanceQuery{}), 1,
		"waking must not also start a second instance for the same key")

	// a message for an UNSEEN key still instantiates — the wake path must not
	// swallow it.
	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "order event", Payload: "ORD-2", CorrelationKey: "ORD-2"}))

	require.Eventually(t, func() bool {
		return len(instanceIDs(t, th, thresher.InstanceQuery{})) == 2
	}, 3*time.Second, 10*time.Millisecond,
		"an unseen key still starts a new instance")
}
