package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// countSawOp counts each time the boundary notification side-flow runs, so a test
// can assert how many behavior throws were caught.
func countSawOp(t *testing.T, count *atomic.Int64) service.Operation {
	t.Helper()

	op, err := gooper.New("count-saw",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			count.Add(1)

			return nil, nil
		})
	require.NoError(t, err)

	return op
}

// completedAtLeast is a boolean expression over the §2.9 numberOfCompletedInstances
// runtime attribute — a Complex behavior condition's quorum test.
func completedAtLeast(t *testing.T, n int) data.FormalExpression {
	t.Helper()

	c, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "numberOfCompletedInstances")
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(int)

			return values.NewVariable(v >= n), nil
		})
	require.NoError(t, err)

	return c
}

// behaviorBoard builds a parallel Multi-Instance review board over three votes with
// a Complex behavior that throws a quorum-reached signal once the completed count
// hits `quorum`, caught by a NON-INTERRUPTING boundary on the MI host whose
// notification side-flow increments `notified` (SRD-056.B). The throw reaches the
// boundary through the real EventHub — the off-loop throw the decorator enables.
func behaviorBoard(
	t *testing.T, name string, quorum int, notified *atomic.Int64,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	// the quorum-reached signal, referenced by both the throw and the catch
	// (matched by name broadcast).
	sig, err := events.NewSignal("quorum-reached", nil)
	require.NoError(t, err)
	throwDef, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)
	catchDef, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	ite, err := events.NewImplicitThrowEvent("quorum", throwDef)
	require.NoError(t, err)
	cbd, err := activities.NewComplexBehaviorDefinition(
		completedAtLeast(t, quorum), ite)
	require.NoError(t, err)

	mi, err := activities.NewMultiInstance(
		activities.WithInputCollection("votes", "v"),
		activities.WithBehavior(activities.BehaviorComplex),
		activities.WithComplexBehavior(cbd))
	require.NoError(t, err)

	body, err := activities.NewSubProcess("board", activities.WithLoop(mi))
	require.NoError(t, err)

	bStart, err := events.NewStartEvent("b-start")
	require.NoError(t, err)
	tally, err := activities.NewServiceTask("tally",
		nopOp(t, "tally-op", 0), activities.WithoutParams())
	require.NoError(t, err)
	bEnd, err := events.NewEndEvent("b-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{bStart, tally, bEnd} {
		require.NoError(t, body.Add(e))
	}
	link(t, bStart, tally)
	link(t, tally, bEnd)

	// a non-interrupting boundary catches the quorum signal → notify → notify-end.
	bnd, err := events.NewBoundaryEvent("quorum-bnd", body, catchDef, false)
	require.NoError(t, err)
	notify, err := activities.NewServiceTask("notify",
		countSawOp(t, notified), activities.WithoutParams())
	require.NoError(t, err)
	nEnd, err := events.NewEndEvent("notify-end")
	require.NoError(t, err)

	p, err := process.New(name, data.WithProperties(data.MustProperty("votes",
		data.MustItemDefinition(values.NewArray(1, 2, 3),
			foundation.WithID("votes")), data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, body, end, bnd, notify, nEnd} {
		require.NoError(t, p.Add(e))
	}
	link(t, start, body)
	link(t, body, end)
	link(t, bnd, notify)
	link(t, notify, nEnd)

	return p
}

// TestMultiInstanceBehaviorE2E (SRD-056.B FR-4/FR-8): a parallel Multi-Instance
// review board throws a quorum-reached Complex behavior signal on every completion
// past the quorum (2 of 3), each caught by the non-interrupting boundary — the
// notification fires once per qualifying completion (completions 2 and 3).
func TestMultiInstanceBehaviorE2E(t *testing.T) {
	var notified atomic.Int64

	p := behaviorBoard(t, "mi-behavior-e2e", 2, &notified)

	require.NoError(t, runFlows(t, p))
	require.Equal(t, int64(2), notified.Load(),
		"the quorum-reached signal is caught on completions 2 and 3")
}

// TestBehaviorLastCompletionCaught (SRD-056.B FR-6): a quorum of 3 (== N) throws
// only on the LAST instance completion — the ordering canary. The event must reach
// the boundary while it is still armed (the throw precedes the activity's move-off),
// so the notification fires exactly once.
func TestBehaviorLastCompletionCaught(t *testing.T) {
	var notified atomic.Int64

	p := behaviorBoard(t, "mi-behavior-last", 3, &notified)

	require.NoError(t, runFlows(t, p))
	require.Equal(t, int64(1), notified.Load(),
		"the event thrown at the last completion is still caught (ordering)")
}
