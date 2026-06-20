package instance

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// runToFailure runs p and asserts it terminates with a fatal error containing
// wantErr (the Complex-join abort path; SRD-023). In-package, so the wiring's
// abort/fail branches are recorded by the per-package coverage profile.
func runToFailure(t *testing.T, p *process.Process, wantErr string) {
	t.Helper()

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		mockeventproc.NewMockEventProducer(t), nil)
	require.NoError(t, err)

	leak := assertNoGoroutineLeak(t)
	defer leak()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, inst.Run(ctx))
	require.Eventually(t,
		func() bool { return inst.State() == Terminated },
		2*time.Second, 5*time.Millisecond,
		"the unsatisfiable Complex join must abort")
	require.ErrorContains(t, inst.LastErr(), wantErr)
}

// TestComplexDiscriminatorInstance: a threshold-1 Complex join fires on the first of
// three parallel arrivals; the other two are consumed as trailing tokens and the
// instance completes (exercises Record, the loop fire via fireOrJoin, the parked
// survivor's resume, and the trailing-token consume — all in-package).
func TestComplexDiscriminatorInstance(t *testing.T) {
	_ = data.CreateDefaultStates()

	p := amountProcess(t, "complex-disc", 0)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	split, err := gateways.NewParallelGateway()
	require.NoError(t, err)
	join, err := gateways.NewComplexGateway(
		gateways.WithActivationThreshold(1),
		gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, join, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, split)
	link(t, split, join)
	link(t, split, join)
	link(t, split, join)
	link(t, join, end)

	runToCompletion(t, p)
}

// TestComplexGuardInstance: a guarded triple (amount<1000, 2) fires at the second of
// three arrivals — exercises the loop-side guard evaluation (guardEval) in-package.
func TestComplexGuardInstance(t *testing.T) {
	_ = data.CreateDefaultStates()

	p := amountProcess(t, "complex-guard", 500)

	tr, err := gateways.NewTriple(2,
		gateways.WithGuard(amountCondT(t, func(a int) bool { return a < 1000 })))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	split, err := gateways.NewParallelGateway()
	require.NoError(t, err)
	join, err := gateways.NewComplexGateway(
		gateways.WithActivation(tr), gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, join, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, split)
	link(t, split, join)
	link(t, split, join)
	link(t, split, join)
	link(t, join, end)

	runToCompletion(t, p)
}

// TestComplexAbortInstance: a threshold-2 join with one branch diverted (XOR default)
// can never reach 2, so the death-recheck aborts and the instance fails — exercises
// recheckJoin's abort branch + fail() in-package.
func TestComplexAbortInstance(t *testing.T) {
	_ = data.CreateDefaultStates()

	p := amountProcess(t, "complex-abort", 500)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	split, err := gateways.NewParallelGateway()
	require.NoError(t, err)
	xor, err := gateways.NewExclusiveGateway()
	require.NoError(t, err)
	join, err := gateways.NewComplexGateway(
		gateways.WithActivationThreshold(2),
		gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)
	end1, err := events.NewEndEvent("end1")
	require.NoError(t, err)
	end2, err := events.NewEndEvent("end2")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, xor, join, end1, end2} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, split)
	link(t, split, join) // branch A: reaches the join
	link(t, split, xor)
	_, err = flow.Link(xor, join,
		flow.WithCondition(amountCondT(t, func(a int) bool { return a > 1000 })))
	require.NoError(t, err)
	df, err := flow.Link(xor, end2) // default: diverts (amount 500) → B never arrives
	require.NoError(t, err)
	require.NoError(t, xor.UpdateDefaultFlow(df))
	link(t, join, end1)

	runToFailure(t, p, "unsatisfiable")
}
