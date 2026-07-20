package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

func linkThrow(t *testing.T, id, name string) *events.IntermediateThrowEvent {
	t.Helper()

	e, err := events.NewIntermediateThrowEvent(
		id, events.MustLinkEventDefinition(name))
	require.NoError(t, err)

	return e
}

func linkCatch(t *testing.T, id, name string) *events.IntermediateCatchEvent {
	t.Helper()

	e, err := events.NewIntermediateCatchEvent(
		id, events.MustLinkEventDefinition(name))
	require.NoError(t, err)

	return e
}

// waitDone drives an instance to completion under a timeout.
func waitDone(t *testing.T, th *thresher.Thresher, procID string) {
	t.Helper()

	h, err := th.StartLatest(procID)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	st, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)
}

// TestLinkThrowToCatch drives a Link GOTO end-to-end (SRD-057 T-6): a token
// flows start → A → throw"L", the throw redirects to the same-name catch"L"'s
// downstream, so B runs and the instance completes.
func TestLinkThrowToCatch(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rec := make(chan string, 4)

	proc, err := process.New("link-goto")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	a := recordTask(t, "A", rec)
	b := recordTask(t, "B", rec)
	thr := linkThrow(t, "thr", "L")
	cat := linkCatch(t, "cat", "L")

	for _, e := range []flow.Element{start, a, thr, cat, b, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, a)
	link(t, a, thr) // A → throw"L" (throw has no outgoing)
	link(t, cat, b) // catch"L" → B (catch has no incoming)
	link(t, b, end)

	th, cancel := runEngine(t, proc)
	defer cancel()

	waitDone(t, th, proc.ID())

	require.Equal(t, []string{"A", "B"}, drain(rec))
}

// TestLinkOnPageLoop proves the redirect is re-entrant (SRD-057 T-6/FR-6): an
// on-page loop of two Link sources (an initial jump + a back-edge) into one
// catch runs a fixed number of iterations, then a data condition exits and the
// instance settles Completed.
func TestLinkOnPageLoop(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rec := make(chan string, 8)

	iterations := 0

	// the loop task records one "iter" and advances the captured counter; the
	// exit condition (evaluated by the gateway after the task) reads the same
	// counter — a single track, so no shared-state race.
	loopTask := recordTask(t, "iter", rec)

	proc, err := process.New("link-loop")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	throwInit := linkThrow(t, "throw-init", "loop")
	throwBack := linkThrow(t, "throw-back", "loop")
	catchLoop := linkCatch(t, "catch-loop", "loop")

	xor, err := gateways.NewExclusiveGateway()
	require.NoError(t, err)

	cond, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			iterations++

			return values.NewVariable(iterations < 3), nil
		})
	require.NoError(t, err)

	for _, e := range []flow.Element{
		start, throwInit, catchLoop, loopTask, xor, throwBack, end,
	} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, throwInit)    // start → throw"loop" (initial jump)
	link(t, catchLoop, loopTask) // catch"loop" → task
	link(t, loopTask, xor)       // task → xor

	_, err = flow.Link(xor, throwBack, flow.WithCondition(cond)) // loop back
	require.NoError(t, err)

	df, err := flow.Link(xor, end) // default → end
	require.NoError(t, err)
	require.NoError(t, xor.UpdateDefaultFlow(df))

	th, cancel := runEngine(t, proc)
	defer cancel()

	waitDone(t, th, proc.ID())

	// both Link sources (initial + back-edge) redirect to the one catch, so the
	// task runs each iteration; the condition exits after the third.
	require.Equal(t, []string{"iter", "iter", "iter"}, drain(rec))
}
