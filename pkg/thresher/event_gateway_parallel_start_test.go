package thresher_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// gateConvKey builds the Parallel-start gate's CorrelationKey: one property with a
// retrieval expression PER arm message (armA/"order A", armB/"order B"), each reading
// the conversation id from that message's payload (item id "<arm>_in", per ebMsgArm) —
// so the starter derives the SAME key from whichever arm fires first, and the rest route
// to that instance (SRD-025 §4.3).
func gateConvKey(t *testing.T) *bpmncommon.CorrelationKey {
	t.Helper()

	mkRE := func(arm, msgName string) bpmncommon.CorrelationPropertyRetrievalExpression {
		itemID := arm + "_in"

		expr := goexpr.Must(nil, data.MustItemDefinition(values.NewVariable("")),
			func(ctx context.Context, ds data.Source) (data.Value, error) {
				d, err := ds.Find(ctx, itemID)
				if err != nil {
					return nil, err
				}

				return values.NewVariable(fmt.Sprint(d.Value().Get(ctx))), nil
			})

		re, err := bpmncommon.NewCorrelationPropertyRetrievalExpression(expr,
			bpmncommon.MustMessage(msgName, data.MustItemDefinition(
				values.NewVariable(""), foundation.WithID(itemID))))
		require.NoError(t, err)

		return *re
	}

	prop, err := bpmncommon.NewCorrelationProperty("conv", "string",
		[]bpmncommon.CorrelationPropertyRetrievalExpression{
			mkRE("armA", "order A"), mkRE("armB", "order B")})
	require.NoError(t, err)

	key, err := bpmncommon.NewCorrelationKey("gateKey",
		[]bpmncommon.CorrelationProperty{*prop})
	require.NoError(t, err)

	return key
}

// instParallelGateProcess builds a process started by an instantiating PARALLEL
// Event-Based gateway (no incoming) with two correlated message arms — the instance is
// born on the first arm, the other arm re-arms keyed to it, and the instance completes
// only once BOTH have fired (SRD-025 §4.3):
//
//	(parallel instantiate gate) ─┬→ catch("order A") → confirmA[A] → endA
//	                             └→ catch("order B") → confirmB[B] → endB
func instParallelGateProcess(t *testing.T, done chan<- string) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New("inst-eb-parallel")
	require.NoError(t, err)

	gate, err := gateways.NewEventBasedGateway(
		gateways.WithInstantiate(),
		gateways.WithEventGatewayType(gateways.ParallelEvents),
		gateways.WithCorrelationKey(gateConvKey(t)))
	require.NoError(t, err)

	armA := ebMsgArm(t, "armA", "order A")
	armB := ebMsgArm(t, "armB", "order B")
	confA := ebMarkerService(t, "confirmA", "A", done)
	confB := ebMarkerService(t, "confirmB", "B", done)

	endA, err := events.NewEndEvent("endA")
	require.NoError(t, err)

	endB, err := events.NewEndEvent("endB")
	require.NoError(t, err)

	for _, e := range []flow.Element{
		gate, armA, armB, confA, confB, endA, endB,
	} {
		require.NoError(t, proc.Add(e))
	}

	for _, l := range [][2]flow.Element{
		{gate, armA}, {armA, confA}, {confA, endA},
		{gate, armB}, {armB, confB}, {confB, endB},
	} {
		_, err := flow.Link(
			l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget))
		require.NoError(t, err)
	}

	return proc
}

// soleInstance returns the single tracked instance handle, failing if there isn't
// exactly one (the Parallel dedup must yield ONE instance, not one per arm).
func soleInstance(t *testing.T, th *thresher.Thresher) *thresher.InstanceHandle {
	t.Helper()

	ids := th.Instances(thresher.InstancesAll)
	require.Len(t, ids, 1, "exactly one instance (Parallel dedup): %v", ids)

	h, ok := th.Instance(ids[0])
	require.True(t, ok)

	return h
}

func runParallelGate(
	t *testing.T, name string, done chan string,
) (*thresher.Thresher, *membroker.Broker, context.Context) {
	t.Helper()

	broker := membroker.New()

	th, err := thresher.New(name, thresher.WithMessageBroker(broker))
	require.NoError(t, err)

	require.NoError(t, th.RegisterProcess(instParallelGateProcess(t, done)))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, th.Run(ctx))

	return th, broker, ctx
}

func awaitMarker(t *testing.T, done <-chan string, want string) {
	t.Helper()

	select {
	case got := <-done:
		require.Equal(t, want, got)
	case <-time.After(3 * time.Second):
		t.Fatalf("arm %q path did not run", want)
	}
}

// TestEventGatewayParallelStartCompletesOnAll: the first arm creates ONE instance; the
// second arm (same key) routes to it; both arms' paths run and the instance completes
// only after both (SRD-025 §4.3).
func TestEventGatewayParallelStartCompletesOnAll(t *testing.T) {
	done := make(chan string, 4)
	th, broker, ctx := runParallelGate(t, "eb-par-all", done)

	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "order A", Payload: "CONV-1", CorrelationKey: "CONV-1"}))
	awaitMarker(t, done, "A")

	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "order B", Payload: "CONV-1", CorrelationKey: "CONV-1"}))
	awaitMarker(t, done, "B")

	h := soleInstance(t, th)
	st, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)
}

// TestEventGatewayParallelStartDoesNotCompleteEarly: publishing only the first arm
// creates the instance and runs that arm, but it stays Active (the other arm's waiting
// track keeps it alive) until the second arm fires — the §2.12.3 completion gate, which
// is automatic via the waiting track keeping active>0 (SRD-025 §10 delta).
func TestEventGatewayParallelStartDoesNotCompleteEarly(t *testing.T) {
	done := make(chan string, 4)
	th, broker, ctx := runParallelGate(t, "eb-par-early", done)

	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "order A", Payload: "CONV-9", CorrelationKey: "CONV-9"}))
	awaitMarker(t, done, "A")

	h := soleInstance(t, th)

	// arm B has not fired → the instance must NOT complete (a short wait times out).
	wctx, wcancel := context.WithTimeout(ctx, 400*time.Millisecond)
	defer wcancel()
	_, werr := h.WaitCompletion(wctx)
	require.Error(t, werr, "instance must not complete before all arms fire")
	require.Equal(t, thresher.StateActive, h.State())

	// arm B fires → routes to this instance → it completes.
	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "order B", Payload: "CONV-9", CorrelationKey: "CONV-9"}))
	awaitMarker(t, done, "B")

	st, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)
}

// TestEventGatewayParallelStartCorrelation: two conversations (CONV-1, CONV-2), arms
// interleaved, must yield two independent instances — each sees only its own key's arms,
// no cross-talk (SRD-025 §4.3, phase-2c most-specific routing).
func TestEventGatewayParallelStartCorrelation(t *testing.T) {
	done := make(chan string, 8)
	th, broker, ctx := runParallelGate(t, "eb-par-corr", done)

	for _, e := range []messaging.Envelope{
		{Name: "order A", Payload: "CONV-1", CorrelationKey: "CONV-1"},
		{Name: "order A", Payload: "CONV-2", CorrelationKey: "CONV-2"},
	} {
		require.NoError(t, broker.Publish(ctx, e))
	}

	// both conversations created their own instance.
	require.Eventually(t, func() bool {
		return len(th.Instances(thresher.InstancesAll)) == 2
	}, 3*time.Second, 10*time.Millisecond, "two instances, one per key")

	for _, e := range []messaging.Envelope{
		{Name: "order B", Payload: "CONV-1", CorrelationKey: "CONV-1"},
		{Name: "order B", Payload: "CONV-2", CorrelationKey: "CONV-2"},
	} {
		require.NoError(t, broker.Publish(ctx, e))
	}

	// four markers total (A,A,B,B); both instances complete.
	got := 0
	for got < 4 {
		select {
		case <-done:
			got++
		case <-time.After(3 * time.Second):
			t.Fatalf("expected 4 arm runs, got %d", got)
		}
	}

	require.Eventually(t, func() bool {
		return len(th.Instances(thresher.InstancesCompleted)) == 2
	}, 3*time.Second, 10*time.Millisecond, "both instances complete on their own arms")
}
