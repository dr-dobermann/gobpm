package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// countingEngine resolves the "x-freeze:expr" language to a fixed candidate list
// and counts every evaluation. The count is the assertion: a frozen triad is
// resolved exactly once, when the task is distributed.
type countingEngine struct {
	evals atomic.Int32
}

func (e *countingEngine) Type() string { return "##XFreeze" }

func (e *countingEngine) Languages() []string { return []string{"x-freeze:expr"} }

func (e *countingEngine) Evaluate(
	_ context.Context, _ data.FormalExpression, _ data.Source,
) (data.Value, error) {
	e.evals.Add(1)

	return values.NewVariable([]string{"alice"}), nil
}

// exprTriadProcess builds a one-UserTask process whose candidate-user slot is an
// EXPRESSION, so the triad must be resolved by the engine rather than read off a
// static list.
func exprTriadProcess(t *testing.T, id string) *process.Process {
	t.Helper()

	proc, err := process.New(id)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	candidates, err := data.NewTextExpression("x-freeze:expr", "candidates",
		data.WithResultType("string"))
	require.NoError(t, err)

	ut, err := activities.NewUserTask("approve",
		activities.WithCandidateUsersExpr(candidates),
		activities.WithOutput("result", "string", true),
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, ut, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, ut)
	link(t, ut, end)

	return proc
}

// TestEligibilityIsResolvedOnceAtDistribution is the freeze proof of ADR-020 v.2
// §2.7 (SRD-073 V2c): an expression-backed triad is evaluated exactly ONCE, when
// the task is distributed, and never again — not by Take, not by Claim, not by
// Complete. The evaluation count is the evidence: if any later check re-resolved,
// the count would rise, and a candidate set computed from process data could then
// shift under a waiting task, revoking an owner's ability to finish work it holds.
func TestEligibilityIsResolvedOnceAtDistribution(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	eng := &countingEngine{}
	cap := &captureDist{}
	proc := exprTriadProcess(t, "ut-freeze")

	th, err := thresher.New("test-freeze",
		thresher.WithTaskDistributor(cap),
		thresher.WithExpressionEngine(eng))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool { return cap.taskID() != "" },
		2*time.Second, 10*time.Millisecond)
	taskID := cap.taskID()

	atDistribution := eng.evals.Load()
	require.Equal(t, int32(1), atDistribution,
		"the triad must be resolved exactly once, at distribution")

	alice := utActor{id: "alice"}
	bob := utActor{id: "bob"}

	// Every subsequent check reads the frozen snapshot.
	_, err = th.Take(ctx, taskID, bob)
	require.Error(t, err)

	_, err = th.Take(ctx, taskID, alice)
	require.NoError(t, err)

	require.Error(t, th.Claim(ctx, taskID, bob))
	require.NoError(t, th.Claim(ctx, taskID, alice))
	require.NoError(t, th.Unclaim(ctx, taskID, alice))
	require.NoError(t, th.Claim(ctx, taskID, alice))

	output := []data.Data{
		data.MustParameter("result",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("ok")),
				data.ReadyDataState)),
	}

	require.Error(t, th.Complete(ctx, taskID, bob, output))
	require.NoError(t, th.Complete(ctx, taskID, alice, output))

	require.Equal(t, atDistribution, eng.evals.Load(),
		"no authorization or ownership check may re-resolve the triad")

	wctx, wc := context.WithTimeout(context.Background(), 3*time.Second)
	defer wc()
	state, err := h.WaitCompletion(wctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, state)
}
