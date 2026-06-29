package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// nopOp is a service operation that does nothing (and optionally sleeps, to keep
// the instance observably mid-flight for snapshot/ctx-cancel tests).
func nopOp(t *testing.T, name string, sleep time.Duration) service.Operation {
	t.Helper()

	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			if sleep > 0 {
				time.Sleep(sleep)
			}

			return nil, nil
		})
	require.NoError(t, err)

	return op
}

// linearProcess builds start -> work(service) -> end.
func linearProcess(t *testing.T, id string, sleep time.Duration) *process.Process {
	t.Helper()

	proc, err := process.New(id)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	work, err := activities.NewServiceTask("work",
		nopOp(t, "work-op", sleep), activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, work, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, work)
	link(t, work, end)

	return proc
}

// parallelProcess builds start -> split =(A,B)=> join -> end (a real fork+join,
// so finished worker tracks are merged at the join).
func parallelProcess(t *testing.T, id string) *process.Process {
	t.Helper()

	proc, err := process.New(id)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	split, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	a, err := activities.NewServiceTask("worker-a",
		nopOp(t, "a-op", 0), activities.WithoutParams())
	require.NoError(t, err)

	b, err := activities.NewServiceTask("worker-b",
		nopOp(t, "b-op", 0), activities.WithoutParams())
	require.NoError(t, err)

	join, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, a, b, join, end} {
		require.NoError(t, proc.Add(e))
	}

	for _, l := range [][2]flow.Element{
		{start, split}, {split, a}, {split, b}, {a, join}, {b, join}, {join, end},
	} {
		link(t, l[0], l[1])
	}

	return proc
}

func link(t *testing.T, from, to flow.Element) {
	t.Helper()

	_, err := flow.Link(from.(flow.SequenceSource), to.(flow.SequenceTarget))
	require.NoError(t, err)
}

// runEngine starts a thresher and registers proc, ready for StartProcess.
func runEngine(t *testing.T, proc *process.Process) (*thresher.Thresher, context.CancelFunc) {
	t.Helper()

	th, err := thresher.New("test-" + proc.ID())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, th.Run(ctx))
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	return th, cancel
}

// TestStartProcessReturnsHandle verifies StartProcess returns a usable handle
// and Thresher.Instance looks it up by id (FR-1, FR-2).
func TestStartProcessReturnsHandle(t *testing.T) {
	proc := linearProcess(t, "h-start", 0)
	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartProcess(proc.ID())
	require.NoError(t, err)
	require.NotNil(t, h)
	require.NotEmpty(t, h.ID())

	got, ok := th.Instance(h.ID())
	require.True(t, ok)
	require.Equal(t, h.ID(), got.ID())

	_, ok = th.Instance("no-such-instance")
	require.False(t, ok)
}

// TestWaitCompletion verifies the handle blocks until terminal state and that a
// cancelled ctx returns its error while the instance is still running
// (FR-3, FR-6).
func TestWaitCompletion(t *testing.T) {
	t.Run("completes", func(t *testing.T) {
		proc := linearProcess(t, "h-wait-ok", 0)
		th, cancel := runEngine(t, proc)
		defer cancel()

		h, err := th.StartProcess(proc.ID())
		require.NoError(t, err)

		ctx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()

		state, err := h.WaitCompletion(ctx)
		require.NoError(t, err)
		require.Equal(t, thresher.StateCompleted, state)
	})

	t.Run("ctx cancel while running", func(t *testing.T) {
		proc := linearProcess(t, "h-wait-cancel", time.Second)
		th, cancel := runEngine(t, proc)
		defer cancel()

		h, err := th.StartProcess(proc.ID())
		require.NoError(t, err)

		ctx, c := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer c()

		state, err := h.WaitCompletion(ctx)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, thresher.StateActive, state)
	})
}

// TestTokensSnapshot verifies Tokens() reports a live (Alive) token at the
// executing node while the instance is mid-flight (FR-4).
func TestTokensSnapshot(t *testing.T) {
	proc := linearProcess(t, "h-tokens", time.Second)
	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartProcess(proc.ID())
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond) // let the token reach the service node

	var alive bool
	for _, tk := range h.Tokens() {
		if tk.State == thresher.TokenAlive {
			alive = true
		}
	}
	require.True(t, alive, "expected a live token while the service task runs")
}

// TestHistoryIncludesMerged verifies History() includes finished (Consumed)
// tracks that Tokens() omits — including the worker tracks merged at the join,
// carrying their fork lineage (FR-4a).
func TestHistoryIncludesMerged(t *testing.T) {
	proc := parallelProcess(t, "h-history")
	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartProcess(proc.ID())
	require.NoError(t, err)

	ctx, c := context.WithTimeout(context.Background(), 3*time.Second)
	defer c()

	state, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, state)

	require.Empty(t, h.Tokens(), "no active tokens after completion")

	hist := h.History()
	require.NotEmpty(t, hist)

	var forked, consumed bool
	for _, p := range hist {
		if p.ParentID != "" {
			forked = true // a worker track forked from the split
		}
		if p.Terminal == thresher.TokenConsumed && len(p.Steps) > 0 {
			consumed = true
		}
	}
	require.True(t, forked, "history must include forked (merged) worker tracks with lineage")
	require.True(t, consumed, "history must include finished (Consumed) tracks")
}

// TestHandleDataRead verifies Data() returns a read-only reader over the
// instance's runtime variables (FR-5).
func TestHandleDataRead(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	proc := linearProcess(t, "h-data", time.Second)
	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartProcess(proc.ID())
	require.NoError(t, err)

	st, err := h.Data().GetData("RUNTIME/STATE")
	require.NoError(t, err)
	require.NotNil(t, st)
}
