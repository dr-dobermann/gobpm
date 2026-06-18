package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// blockingProcess builds start -> work(blocks until its ctx is cancelled) -> end,
// so the instance stays Active until cancelled — and then terminates promptly
// (the op respects ctx).
func blockingProcess(t *testing.T, id string) *process.Process {
	t.Helper()

	op, err := gooper.New("block-op",
		func(ctx context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			<-ctx.Done()

			return nil, nil
		})
	require.NoError(t, err)

	proc, err := process.New(id)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	work, err := activities.NewServiceTask("work", op, activities.WithoutParams())
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

// TestInstanceHandleCancel verifies Cancel drives a running instance to a
// terminal state and is idempotent (FR-1, FR-2).
func TestInstanceHandleCancel(t *testing.T) {
	proc := blockingProcess(t, "ctl-cancel")
	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartProcess(proc.ID())
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond) // let the track reach the blocking op

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()

	state, err := h.Cancel(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateTerminated, state)

	// Idempotent: a second Cancel (and Cancel of an already-terminal instance)
	// returns the terminal state at once.
	state2, err := h.Cancel(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateTerminated, state2)
}

// TestCancelCtxBounded verifies Cancel honours its ctx deadline when the instance
// won't settle promptly (here a ctx-ignoring sleep keeps it from terminating),
// returning ctx.Err() and a non-terminal state (FR-1).
func TestCancelCtxBounded(t *testing.T) {
	proc := linearProcess(t, "ctl-cancel-bound", 2*time.Second) // op ignores ctx
	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartProcess(proc.ID())
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond)

	ctx, cc := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cc()

	state, err := h.Cancel(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NotEqual(t, thresher.StateTerminated, state)
	require.NotEqual(t, thresher.StateCompleted, state)
}

// TestSuspendResumeReserved verifies the reserved control ops return the stable
// ErrNotImplemented sentinel (FR-3).
func TestSuspendResumeReserved(t *testing.T) {
	proc := blockingProcess(t, "ctl-reserved")
	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartProcess(proc.ID())
	require.NoError(t, err)

	require.ErrorIs(t, h.Suspend(context.Background()), thresher.ErrNotImplemented)
	require.ErrorIs(t, h.Resume(context.Background()), thresher.ErrNotImplemented)
}

// TestThresherShutdown verifies graceful shutdown cancels running instances,
// flips to Stopped, drains the hub, then rejects further lifecycle ops, and is
// idempotent (FR-4, FR-5, FR-6).
func TestThresherShutdown(t *testing.T) {
	proc := blockingProcess(t, "sd-graceful")
	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartProcess(proc.ID())
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond) // reach the blocking op

	sctx, sc := context.WithTimeout(context.Background(), 3*time.Second)
	defer sc()
	require.NoError(t, th.Shutdown(sctx))

	require.Equal(t, thresher.Stopped, th.State())
	require.Equal(t, thresher.StateTerminated, h.State())

	// A stopped engine rejects further lifecycle operations.
	_, err = th.StartProcess(proc.ID())
	require.Error(t, err)
	require.Error(t, th.RegisterProcess(blockingProcess(t, "sd-after")))
	require.Error(t, th.Run(context.Background()))

	// Idempotent.
	require.NoError(t, th.Shutdown(sctx))
}

// TestThresherShutdownCtxBounded verifies Shutdown honours its ctx deadline when
// an instance will not settle promptly (a ctx-ignoring sleep), returning an
// error while still having flipped to Stopped (FR-4, NFR-3).
func TestThresherShutdownCtxBounded(t *testing.T) {
	proc := linearProcess(t, "sd-bound", 2*time.Second) // op ignores ctx
	th, cancel := runEngine(t, proc)
	defer cancel()

	_, err := th.StartProcess(proc.ID())
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond)

	sctx, sc := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer sc()

	require.Error(t, th.Shutdown(sctx))
	require.Equal(t, thresher.Stopped, th.State())
}
