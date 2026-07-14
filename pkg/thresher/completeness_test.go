package thresher_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/dr-dobermann/gobpm/pkg/tasks/localdispatcher"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// boundaryFaultProcess builds start → host(raises a BpmnError) → normal-end,
// with an interrupting Error boundary on host whose errorRef matches the raised
// code routing to exc-end. Running it emits Fault (Thrown/Caught) — an Error
// boundary is matched at failure time, not armed as a hub waiter, so it emits no
// Boundary facts (see timerBoundaryProcess for those). Adapted from
// internal/instance/boundary_error_test.go's errorGuardedInstance, lifted to a
// *process.Process builder.
func boundaryFaultProcess(t *testing.T, id, code string) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New(id)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	raise, err := gooper.New("raise-"+code,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return nil, &events.BpmnError{Code: code}
		})
	require.NoError(t, err)

	host, err := activities.NewServiceTask("host", raise,
		activities.WithoutParams())
	require.NoError(t, err)

	normalEnd, err := events.NewEndEvent("normal-end")
	require.NoError(t, err)

	bpErr, err := bpmncommon.NewError("boundary-error", code, nil)
	require.NoError(t, err)

	eed, err := events.NewErrorEventDefinition(bpErr)
	require.NoError(t, err)

	be, err := events.NewBoundaryEvent("err-bnd", host, eed, true)
	require.NoError(t, err)

	excEnd, err := events.NewEndEvent("exc-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, host, normalEnd, be, excEnd} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, host)
	link(t, host, normalEnd)
	link(t, be, excEnd)

	return proc
}

// dualCoverKey builds a CorrelationKey whose single "orderId" property extracts
// the payload (bound under item id "order_in") as the key, with one retrieval
// expression per message name — so EVERY named message covers the key. This lets
// both the instantiating start message AND the in-instance follow-up message
// derive the key, so the follow-up hits Correlation/Matched. Mirrors the instance
// package's testCorrKey (internal/instance/conversation_key_test.go).
func dualCoverKey(t *testing.T, msgNames ...string) *bpmncommon.CorrelationKey {
	t.Helper()

	res := make(
		[]bpmncommon.CorrelationPropertyRetrievalExpression, 0, len(msgNames))

	for _, mn := range msgNames {
		mp := goexpr.Must(nil, data.MustItemDefinition(values.NewVariable("")),
			func(ctx context.Context, ds data.Source) (data.Value, error) {
				d, err := ds.Find(ctx, "order_in")
				if err != nil {
					return nil, err
				}

				return values.NewVariable(fmt.Sprint(d.Value().Get(ctx))), nil
			})

		re, err := bpmncommon.NewCorrelationPropertyRetrievalExpression(mp,
			bpmncommon.MustMessage(mn, data.MustItemDefinition(
				values.NewVariable(""), foundation.WithID("order_in"))))
		require.NoError(t, err)

		res = append(res, *re)
	}

	prop, err := bpmncommon.NewCorrelationProperty("orderId", "string", res)
	require.NoError(t, err)

	key, err := bpmncommon.NewCorrelationKey("orderKey",
		[]bpmncommon.CorrelationProperty{*prop})
	require.NoError(t, err)

	return key
}

// correlationMatchProcess builds a keyed message-start process whose in-instance
// ReceiveTask awaits a follow-up message the SAME correlation key also covers:
//
//	start(startMsg, keyed by orderId covering both messages)
//	  → recv(followMsg) → end
//
// The start message instantiates + KeyAssociates (orderId = payload) and parks
// the receiver keyed on that value; the follow-up message routes to that parked
// receiver, where the instance re-derives orderId from its payload and finds it
// equals the held value — no mismatch, key derived — emitting Correlation/Matched
// (SRD-017 §4.5). Both messages bind their payload under item id "order_in", so
// the shared retrieval expression reads either.
func correlationMatchProcess(
	t *testing.T, id, startMsg, followMsg string,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New(id)
	require.NoError(t, err)

	// The key is declared on the start event (to seed the conversation on
	// instantiation) AND as a process CorrelationSubscription — the latter is what
	// lands in the snapshot's CorrelationKeys, which validateAndAssociate reads to
	// derive Matched on the follow-up (snapshot.correlationKeys reads
	// p.CorrelationSubscriptions, not the start-event key).
	key := dualCoverKey(t, startMsg, followMsg)
	proc.CorrelationSubscriptions = []*bpmncommon.CorrelationSubscription{
		{Key: key},
	}

	start, err := events.NewStartEvent("start",
		events.WithMessageTrigger(events.MustMessageEventDefinition(
			bpmncommon.MustMessage(startMsg, data.MustItemDefinition(
				values.NewVariable(""), foundation.WithID("order_in"))), nil)),
		events.WithCorrelationKey(key))
	require.NoError(t, err)

	recv, err := activities.NewReceiveTask("await-follow",
		bpmncommon.MustMessage(followMsg, data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID("order_in"))),
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, recv, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, recv)
	link(t, recv, end)

	return proc
}

// timerBoundaryProcess builds start → host(sleeps 2s) → end-paid, with an
// interrupting timer boundary (200ms) on host → cancel → end-cancelled. The
// timer fires long before the slow host finishes, so it emits Boundary
// (Armed/Fired/Disarmed). An Error boundary is NOT armed as a hub waiter
// (boundary_watch.go §4.4), so it emits no Boundary facts — a timer boundary is
// the one that does. Cribbed from examples/boundary-events/process.go.
func timerBoundaryProcess(t *testing.T, id string) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New(id)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	host, err := activities.NewServiceTask("host",
		nopOp(t, "slow-op", 2*time.Second), activities.WithoutParams())
	require.NoError(t, err)

	endPaid, err := events.NewEndEvent("end-paid")
	require.NoError(t, err)

	cancelOrder, err := activities.NewServiceTask("cancel-order",
		nopOp(t, "cancel-op", 0), activities.WithoutParams())
	require.NoError(t, err)

	endCancelled, err := events.NewEndEvent("end-cancelled")
	require.NoError(t, err)

	be := interruptingTimerBoundary(t, "timeout", host, 200*time.Millisecond)

	for _, e := range []flow.Element{
		start, host, endPaid, cancelOrder, endCancelled, be,
	} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, host)
	link(t, host, endPaid)
	link(t, be, cancelOrder)
	link(t, cancelOrder, endCancelled)

	return proc
}

// interruptingTimerBoundary builds an interrupting timer boundary firing d after
// its host is entered.
func interruptingTimerBoundary(
	t *testing.T, id string, host flow.ActivityNode, d time.Duration,
) *events.BoundaryEvent {
	t.Helper()

	when := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(time.Now().Add(d))),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(time.Now().Add(d)), nil
		},
		foundation.WithID(id+"-at"))

	def, err := events.NewTimerEventDefinition(when, nil, nil)
	require.NoError(t, err)

	be, err := events.NewBoundaryEvent(id, host, def, true) // interrupting
	require.NoError(t, err)

	return be
}

// workerProcess builds start → svc(routed to an external worker on topic) → end.
// Run under a localdispatcher whose worker returns cleanly, it emits JobState
// (Enqueued/Locked/Completed).
func workerProcess(t *testing.T, id, topic string) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New(id)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	svc, err := activities.NewServiceTask("svc",
		service.MustOperation("op", nil, nil, nil),
		activities.WithWorker(topic), activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, svc, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, svc)
	link(t, svc, end)

	return proc
}

// TestEngineScopeEmissionCompleteness (SRD-041 T-4, the "emission completeness"
// canary): one engine-scope observer, registered before Run, must see facts
// covering ALL 13 observability catalog kinds across a set of focused
// sub-scenarios launched under it. The observer sees every instance under the
// engine, so the union of what the sub-scenarios emit is what we assert.
// KindDataChange — deferred by SRD-041 — is wired by SRD-044 (the ADR-011
// v.6 §2.9.4 commit-diff) and asserted like the rest.
func TestEngineScopeEmissionCompleteness(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	broker := membroker.New()
	disp := localdispatcher.New(nil, 0)
	dist := &captureDist{}

	th, err := thresher.New("completeness-engine",
		thresher.WithMessageBroker(broker),
		thresher.WithWorkerDispatcher(disp),
		thresher.WithTaskDistributor(dist))
	require.NoError(t, err)

	c := &collector{}
	sub := th.Observe(c) // engine-scope, before Run — sees the whole lifecycle

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// the pool worker returns cleanly, so its job reaches JobState/Completed.
	require.NoError(t, disp.RegisterWorker(ctx, "topic-x",
		func(context.Context, tasks.LockedJob) (*data.ItemDefinition, error) {
			return nil, nil
		}))

	require.NoError(t, th.Run(ctx))

	runGatewayScenario(t, ctx, th)
	runWorkerScenario(t, ctx, th)
	runTimerBoundaryScenario(t, ctx, th)
	runErrorFaultScenario(t, ctx, th)
	runCorrelationScenario(t, ctx, th, broker)
	runUserTaskScenario(t, ctx, th, dist)

	require.NoError(t, th.Shutdown(context.Background()))

	sub.Cancel() // drains the buffered facts before we assert

	assertAll13Kinds(t, c)
}
