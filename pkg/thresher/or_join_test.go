package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// TestORJoinUntakenBranch exercises the OR-join firing on a never-taken branch,
// with no token death (SRD-022 FR-7):
//
//	        ┌[amount<100]→ A ─┐
//	start ─OR              OR-join → end
//	        ├[amount>10] → B ─┤
//	        └[amount>1000]→ C ┘   (C not taken)
//
// amount 50 ⟹ A and B fork and arrive at the join (2 of 3 marked) and park; C is
// never taken, so its incoming flow is unreachable and the loop's parking recheck
// fires the join — the survivor continues to the end and the instance completes
// exactly once (no hang).
func TestORJoinUntakenBranch(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rec := make(chan string, 8)
	proc := amountProcess(t, "or-join-untaken", 50)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	split, err := gateways.NewInclusiveGateway(gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)
	join, err := gateways.NewInclusiveGateway(gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)
	a := recordTask(t, "a", rec)
	b := recordTask(t, "b", rec)
	c := recordTask(t, "c", rec) // never taken
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, join, a, b, c, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, split)
	_, err = flow.Link(split, a, flow.WithCondition(amountCond(t, func(x int) bool { return x < 100 })))
	require.NoError(t, err)
	_, err = flow.Link(split, b, flow.WithCondition(amountCond(t, func(x int) bool { return x > 10 })))
	require.NoError(t, err)
	_, err = flow.Link(split, c, flow.WithCondition(amountCond(t, func(x int) bool { return x > 1000 })))
	require.NoError(t, err)
	link(t, a, join)
	link(t, b, join)
	link(t, c, join)
	link(t, join, end)

	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	st, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)

	// Both taken branches ran before the join; C never did. The join fired and
	// the survivor reached the end (the instance completed, no hang).
	require.Equal(t, []string{"a", "b"}, drain(rec))
}

// TestORJoinDeathTriggered exercises the anti-hang (SRD-022 FR-4): an awaited
// branch is activated but diverts away from the join and dies, so the join never
// gets that arrival — the loop's death-recheck must still fire it.
//
//	start ─OR┬[<100]→ A ───────────────→ OR-join → end1
//	         └[>10] → XOR┬[>1000]→ B → ──┘
//	                     └default → end2   (taken: B never reaches the join)
//
// amount 50 ⟹ A parks at the join; the XOR branch takes default to end2 (B never
// runs), so B's incoming flow is never marked. Only when that branch dies at end2
// does the death-recheck find the flow unreachable and fire the join.
func TestORJoinDeathTriggered(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rec := make(chan string, 8)
	proc := amountProcess(t, "or-join-death", 50)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	orSplit, err := gateways.NewInclusiveGateway(gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)
	xor, err := gateways.NewExclusiveGateway()
	require.NoError(t, err)
	join, err := gateways.NewInclusiveGateway(gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)
	a := recordTask(t, "a", rec)
	b := recordTask(t, "b", rec) // structurally feeds the join, but never taken
	end1, err := events.NewEndEvent("end1")
	require.NoError(t, err)
	end2, err := events.NewEndEvent("end2")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, orSplit, xor, join, a, b, end1, end2} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, orSplit)
	_, err = flow.Link(orSplit, a, flow.WithCondition(amountCond(t, func(x int) bool { return x < 100 })))
	require.NoError(t, err)
	_, err = flow.Link(orSplit, xor, flow.WithCondition(amountCond(t, func(x int) bool { return x > 10 })))
	require.NoError(t, err)
	_, err = flow.Link(xor, b, flow.WithCondition(amountCond(t, func(x int) bool { return x > 1000 })))
	require.NoError(t, err)
	df, err := flow.Link(xor, end2) // default: the branch diverts here and dies
	require.NoError(t, err)
	require.NoError(t, xor.UpdateDefaultFlow(df))
	link(t, a, join)
	link(t, b, join)
	link(t, join, end1)

	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	st, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)

	// A ran and survived the join; B was never taken. The join fired only because
	// the diverted branch died — without the death-recheck the instance hangs.
	require.Equal(t, []string{"a"}, drain(rec))
}

// TestORJoinAllBranchesArrive locks FIX-006: when every OR-split branch is taken, the
// join completes on the LAST arrival (the all-marked path), and the earlier arrivals,
// parked on AwaitSync, must be woken so their goroutines return. Before the fix the
// arrival-complete path never signaled their parkCh and the instance hung.
func TestORJoinAllBranchesArrive(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rec := make(chan string, 8)
	proc := amountProcess(t, "or-join-all", 50)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	split, err := gateways.NewInclusiveGateway(gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)
	join, err := gateways.NewInclusiveGateway(gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)
	a := recordTask(t, "a", rec)
	b := recordTask(t, "b", rec)
	c := recordTask(t, "c", rec)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, join, a, b, c, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, split)
	_, err = flow.Link(split, a, flow.WithCondition(amountCond(t, func(x int) bool { return x < 100 })))
	require.NoError(t, err)
	_, err = flow.Link(split, b, flow.WithCondition(amountCond(t, func(x int) bool { return x > 10 })))
	require.NoError(t, err)
	_, err = flow.Link(split, c, flow.WithCondition(amountCond(t, func(x int) bool { return x >= 0 })))
	require.NoError(t, err)
	link(t, a, join)
	link(t, b, join)
	link(t, c, join)
	link(t, join, end)

	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	st, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)
	require.ElementsMatch(t, []string{"a", "b", "c"}, drain(rec))
}
