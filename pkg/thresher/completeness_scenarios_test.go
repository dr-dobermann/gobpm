package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// runGatewayScenario launches a parallel fork+join process. It contributes
// InstanceState (Created/Active/Completed), NodeProgress (Entered/Executing/
// Completed) and GatewayDecision (BranchesChosen).
func runGatewayScenario(
	t *testing.T, ctx context.Context, th *thresher.Thresher,
) {
	t.Helper()

	proc := parallelProcess(t, "cmp-gateway")
	_, err := th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := h.WaitCompletion(wctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, state)
}

// runWorkerScenario launches a process whose ServiceTask is routed to the
// engine's localdispatcher worker (topic-x). It contributes JobState
// (Enqueued/Locked/Completed) — the dispatcher's reporter is bound to the
// engine producer, so those facts reach the engine observer.
func runWorkerScenario(
	t *testing.T, ctx context.Context, th *thresher.Thresher,
) {
	t.Helper()

	proc := workerProcess(t, "cmp-worker", "topic-x")
	_, err := th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := h.WaitCompletion(wctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, state)
}

// runTimerBoundaryScenario launches a process whose slow activity is interrupted
// by a firing timer boundary. It contributes Boundary (Armed/Fired/Disarmed) —
// an Error boundary is not armed as a hub waiter, so a timer boundary is the one
// that emits these facts.
func runTimerBoundaryScenario(
	t *testing.T, ctx context.Context, th *thresher.Thresher,
) {
	t.Helper()

	proc := timerBoundaryProcess(t, "cmp-timer-boundary")
	_, err := th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := h.WaitCompletion(wctx)
	require.NoError(t, err)
	// the timer fires, cancels the slow host, and routes to the cancel flow.
	require.Equal(t, thresher.StateCompleted, state)
}

// runErrorFaultScenario launches a process whose activity raises a BpmnError
// caught by a matching interrupting Error boundary. It contributes Fault
// (Thrown/Caught). (An Error boundary emits no Boundary facts — it is matched at
// failure time, not armed as a hub waiter; boundary_watch.go §4.4.)
func runErrorFaultScenario(
	t *testing.T, ctx context.Context, th *thresher.Thresher,
) {
	t.Helper()

	proc := boundaryFaultProcess(t, "cmp-fault", "E-CMP")
	_, err := th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := h.WaitCompletion(wctx)
	require.NoError(t, err)
	// a caught BpmnError routes to the exception flow and completes normally.
	require.Equal(t, thresher.StateCompleted, state)
}

// runCorrelationScenario drives two message flows under the engine:
//
//  1. orderConversationProcess — a keyed message start instantiates a handler
//     (KeyAssociated) that parks an in-instance ReceiveTask, then a follow-up
//     "payment received" message routes back to it. This contributes
//     Correlation/KeyAssociated and EventFlow (Registered/Fired/Delivered).
//  2. correlationMatchProcess — a keyed start whose correlation key ALSO covers
//     its in-instance follow-up message, so when the follow-up routes to the
//     parked receiver the instance re-derives its held key (no mismatch, key
//     derived), emitting Correlation/Matched (SRD-017 §4.5).
func runCorrelationScenario(
	t *testing.T, ctx context.Context, th *thresher.Thresher,
	broker *membroker.Broker,
) {
	t.Helper()

	// Flow 1: conversation → EventFlow + KeyAssociated.
	done := make(chan string, 1)
	_, err := th.RegisterProcess(orderConversationProcess(t, done))
	require.NoError(t, err)

	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "ORD-C", CorrelationKey: "ORD-C"}))

	// give the handler a moment to reach and park its payment receiver.
	time.Sleep(200 * time.Millisecond)

	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "payment received", Payload: "ORD-C", CorrelationKey: "ORD-C"}))

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the correlation handler did not complete")
	}

	// Flow 2: match → Correlation/Matched. The start instantiates and keys the
	// handler on BK-1 and parks its follow receiver; the follow-up (whose payload
	// the shared key also derives) routes to it and re-derives the held key.
	_, err = th.RegisterProcess(correlationMatchProcess(
		t, "cmp-corr-match", "order booked", "order shipped"))
	require.NoError(t, err)

	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "order booked", Payload: "BK-1", CorrelationKey: "BK-1"}))

	// let the handler park its "order shipped" receiver keyed on BK-1.
	time.Sleep(200 * time.Millisecond)

	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "order shipped", Payload: "BK-1", CorrelationKey: "BK-1"}))

	// let the follow-up be validated and the instance complete before teardown.
	time.Sleep(300 * time.Millisecond)
}

// runUserTaskScenario launches a UserTask process and drives it Announced →
// Taken → Completed through the engine's Take/Complete APIs. It contributes
// TaskState (Announced/Taken/Completed). It reuses the engine's shared
// captureDist to learn the engine-minted task id.
func runUserTaskScenario(
	t *testing.T, ctx context.Context, th *thresher.Thresher,
	dist *captureDist,
) {
	t.Helper()

	proc := userTaskProcess(t, "cmp-usertask")
	_, err := th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	// the UserTask parks and is announced to the distributor.
	require.Eventually(t, func() bool { return dist.taskID() != "" },
		2*time.Second, 10*time.Millisecond)
	taskID := dist.taskID()

	alice := utActor{id: "alice"}
	_, err = th.Take(ctx, taskID, alice)
	require.NoError(t, err)

	output := []data.Data{
		data.MustParameter("result",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("ok")),
				data.ReadyDataState)),
	}
	require.NoError(t, th.Complete(ctx, taskID, alice, output))

	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := h.WaitCompletion(wctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, state)
}

// assertAll12Kinds asserts the collector saw every one of the 12 landed
// catalog kinds, and — for the richer ones — a representative (kind, phase)
// pair, so the canary proves real coverage, not mere kind presence.
func assertAll12Kinds(t *testing.T, c *collector) {
	t.Helper()

	seen := c.kinds()

	for _, k := range []observability.Kind{
		observability.KindEngineState,
		observability.KindHubState,
		observability.KindProcessLifecycle,
		observability.KindInstanceState,
		observability.KindNodeProgress,
		observability.KindGatewayDecision,
		observability.KindEventFlow,
		observability.KindCorrelation,
		observability.KindJobState,
		observability.KindTaskState,
		observability.KindBoundary,
		observability.KindFault,
	} {
		require.Truef(t, seen[k],
			"engine-scope observer never saw a %s fact", k)
	}

	// Representative phases prove the richer kinds carried real progress, not
	// just any single fact.
	require.True(t, c.sawKindPhase(
		observability.KindGatewayDecision, observability.PhaseBranchesChosen),
		"GatewayDecision/BranchesChosen missing")
	require.True(t, c.sawKindPhase(
		observability.KindFault, observability.PhaseCaught),
		"Fault/Caught missing")
	require.True(t, c.sawKindPhase(
		observability.KindBoundary, observability.PhaseFired),
		"Boundary/Fired missing")
	require.True(t, c.sawKindPhase(
		observability.KindCorrelation, observability.PhaseMatched),
		"Correlation/Matched missing")
	require.True(t, c.sawKindPhase(
		observability.KindJobState, observability.PhaseCompleted),
		"JobState/Completed missing")
	require.True(t, c.sawKindPhase(
		observability.KindTaskState, observability.PhaseCompleted),
		"TaskState/Completed missing")
}
