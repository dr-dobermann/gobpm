package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// SRD-059 T-10 — Compensation end-to-end through the public engine surface,
// with the Thrown → Compensating → Compensated triple asserted via th.Observe.

// compGuardE2E attaches a Compensation boundary on host routing to a fresh
// counting handler; returns the nodes to Add.
func compGuardE2E(
	t *testing.T, host flow.ActivityNode, name string, ran *atomic.Bool,
) []flow.Element {
	t.Helper()

	op, err := gooper.New(name+"-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			ran.Store(true)

			return nil, nil
		})
	require.NoError(t, err)

	handler, err := activities.NewServiceTask(name, op,
		activities.WithoutParams(), activities.WithCompensation())
	require.NoError(t, err)

	ced, err := events.NewCompensationEventDefinition(nil, true)
	require.NoError(t, err)
	bnd, err := events.NewCompensationBoundaryEvent(
		"comp-"+name, host, ced, handler)
	require.NoError(t, err)

	return []flow.Element{bnd, handler}
}

// TestCompensationE2E (T-10): a completed guarded task is compensated by a
// scope-wide wait-for-completion End-Event throw; the instance completes and
// the observer sees the full fact triple.
func TestCompensationE2E(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var worked, undone atomic.Bool

	proc, err := process.New("comp-e2e")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	work := laneTask(t, "work", &worked)

	ced, err := events.NewCompensationEventDefinition(nil, true)
	require.NoError(t, err)
	throwEnd, err := events.NewEndEvent("comp-end",
		events.WithCompensationTrigger(ced))
	require.NoError(t, err)

	for _, e := range append([]flow.Element{start, work, throwEnd},
		compGuardE2E(t, work, "undo-work", &undone)...) {
		require.NoError(t, proc.Add(e))
	}
	link(t, start, work)
	link(t, work, throwEnd)

	th, err := thresher.New("comp-e2e-engine")
	require.NoError(t, err)

	c := &collector{}
	sub := th.Observe(c)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, th.Run(ctx))
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	defer wcancel()
	state, werr := h.WaitCompletion(wctx)
	require.NoError(t, werr)
	require.Equal(t, thresher.StateCompleted, state)

	require.NoError(t, th.Shutdown(context.Background()))
	sub.Cancel() // drains buffered facts

	require.True(t, worked.Load())
	require.True(t, undone.Load(), "the compensation handler ran")

	for _, ph := range []observability.Phase{
		observability.PhaseEligible, observability.PhaseThrown,
		observability.PhaseCompensating, observability.PhaseCompensated,
	} {
		require.True(t,
			c.sawKindPhase(observability.KindCompensation, ph),
			string(ph))
	}
}
