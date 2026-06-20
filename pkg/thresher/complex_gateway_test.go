package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

func count(ss []string, s string) int {
	n := 0

	for _, x := range ss {
		if x == s {
			n++
		}
	}

	return n
}

// parallelSplit forks all of its outgoing flows (ADR-005 §2.2), activating every
// branch unconditionally — the cleanest way to feed a Complex join in a test.
func parallelSplit(t *testing.T) *gateways.ParallelGateway {
	t.Helper()

	pg, err := gateways.NewParallelGateway()
	require.NoError(t, err)

	return pg
}

func complexJoin(t *testing.T, act gateways.ComplexOption) *gateways.ComplexGateway {
	t.Helper()

	cg, err := gateways.NewComplexGateway(act, gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)

	return cg
}

// TestComplexDiscriminator: a Complex join with threshold 1 fires on the first of
// three parallel arrivals (the Structured Discriminator, WCP-9); the other two arrive
// later and are consumed (trailing tokens), and the instance completes once.
func TestComplexDiscriminator(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rec := make(chan string, 16)
	proc := amountProcess(t, "complex-discriminator", 0)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	split := parallelSplit(t)
	a := recordTask(t, "a", rec)
	b := recordTask(t, "b", rec)
	c := recordTask(t, "c", rec)
	join := complexJoin(t, gateways.WithActivationThreshold(1))
	finalize := recordTask(t, "finalize", rec)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, a, b, c, join, finalize, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, split)
	link(t, split, a)
	link(t, split, b)
	link(t, split, c)
	link(t, a, join)
	link(t, b, join)
	link(t, c, join)
	link(t, join, finalize)
	link(t, finalize, end)

	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartProcess(proc.ID())
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	st, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)

	got := drain(rec)
	require.Equal(t, 1, count(got, "finalize"), "the join fires exactly once")
	for _, br := range []string{"a", "b", "c"} {
		require.Contains(t, got, br, "every branch ran before the join")
	}
}

// TestComplexPartialJoin: threshold 2 fires on the second of three arrivals (the
// Structured Partial Join, WCP-30); the third is consumed; the instance completes.
func TestComplexPartialJoin(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rec := make(chan string, 16)
	proc := amountProcess(t, "complex-partial", 0)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	split := parallelSplit(t)
	a := recordTask(t, "a", rec)
	b := recordTask(t, "b", rec)
	c := recordTask(t, "c", rec)
	join := complexJoin(t, gateways.WithActivationThreshold(2))
	finalize := recordTask(t, "finalize", rec)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, a, b, c, join, finalize, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, split)
	link(t, split, a)
	link(t, split, b)
	link(t, split, c)
	link(t, a, join)
	link(t, b, join)
	link(t, c, join)
	link(t, join, finalize)
	link(t, finalize, end)

	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartProcess(proc.ID())
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	st, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)

	require.Equal(t, 1, count(drain(rec), "finalize"))
}

// TestComplexDataAware: the activation rule [(amount<1000, 2), (amount>=1000, 3)]
// makes the threshold data-dependent; amount 500 fires at 2.
func TestComplexDataAware(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rec := make(chan string, 16)
	proc := amountProcess(t, "complex-data", 500)

	small, err := gateways.NewTriple(2,
		gateways.WithGuard(amountCond(t, func(a int) bool { return a < 1000 })))
	require.NoError(t, err)
	big, err := gateways.NewTriple(3,
		gateways.WithGuard(amountCond(t, func(a int) bool { return a >= 1000 })))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	split := parallelSplit(t)
	a := recordTask(t, "a", rec)
	b := recordTask(t, "b", rec)
	c := recordTask(t, "c", rec)
	join := complexJoin(t, gateways.WithActivation(small, big))
	finalize := recordTask(t, "finalize", rec)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, a, b, c, join, finalize, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, split)
	link(t, split, a)
	link(t, split, b)
	link(t, split, c)
	link(t, a, join)
	link(t, b, join)
	link(t, c, join)
	link(t, join, finalize)
	link(t, finalize, end)

	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartProcess(proc.ID())
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	st, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)
	require.Equal(t, 1, count(drain(rec), "finalize"))
}

// TestComplexRequiredGate: a triple (count 2, required = a's incoming flow) fires only
// when a is among the two arrivals; the instance completes once.
func TestComplexRequiredGate(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rec := make(chan string, 16)
	proc := amountProcess(t, "complex-required", 0)

	tr, err := gateways.NewTriple(2, gateways.WithRequired("a-join"))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	split := parallelSplit(t)
	a := recordTask(t, "a", rec)
	b := recordTask(t, "b", rec)
	c := recordTask(t, "c", rec)
	join := complexJoin(t, gateways.WithActivation(tr))
	finalize := recordTask(t, "finalize", rec)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, a, b, c, join, finalize, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, split)
	link(t, split, a)
	link(t, split, b)
	link(t, split, c)
	_, err = flow.Link(a, join, foundation.WithID("a-join")) // the required gate
	require.NoError(t, err)
	link(t, b, join)
	link(t, c, join)
	link(t, join, finalize)
	link(t, finalize, end)

	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartProcess(proc.ID())
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	st, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)

	got := drain(rec)
	require.Equal(t, 1, count(got, "finalize"))
	require.Contains(t, got, "a") // the required branch ran
}

// TestComplexAbortOnDeath: threshold 3 needs all three branches, but one diverts
// through an XOR to a separate end and dies — its incoming flow becomes unreachable,
// so the death-recheck aborts the join (the instance fails, no hang).
func TestComplexAbortOnDeath(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rec := make(chan string, 16)
	proc := amountProcess(t, "complex-abort", 500)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	split := parallelSplit(t)
	a := recordTask(t, "a", rec)
	b := recordTask(t, "b", rec)
	xor, err := gateways.NewExclusiveGateway()
	require.NoError(t, err)
	c := recordTask(t, "c", rec) // only reached if the XOR takes its >1000 branch
	join := complexJoin(t, gateways.WithActivationThreshold(3))
	finalize := recordTask(t, "finalize", rec)
	end1, err := events.NewEndEvent("end1")
	require.NoError(t, err)
	end2, err := events.NewEndEvent("end2")
	require.NoError(t, err)

	for _, e := range []flow.Element{
		start, split, a, b, xor, c, join, finalize, end1, end2,
	} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, split)
	link(t, split, a)
	link(t, split, b)
	link(t, split, xor)
	_, err = flow.Link(xor, c,
		flow.WithCondition(amountCond(t, func(a int) bool { return a > 1000 })))
	require.NoError(t, err)
	df, err := flow.Link(xor, end2) // default: diverts and dies (amount 500)
	require.NoError(t, err)
	require.NoError(t, xor.UpdateDefaultFlow(df))
	link(t, a, join)
	link(t, b, join)
	link(t, c, join)
	link(t, join, finalize)
	link(t, finalize, end1)

	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartProcess(proc.ID())
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	st, err := h.WaitCompletion(ctx)
	require.Equal(t, thresher.StateTerminated, st, "the join cannot reach 3 → abort")
	require.ErrorContains(t, err, "unsatisfiable")

	require.Equal(t, 0, count(drain(rec), "finalize"), "the join never fires")
}
