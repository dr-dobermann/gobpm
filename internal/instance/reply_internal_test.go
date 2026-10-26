package instance

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/stretchr/testify/require"
)

// TestAwaitReplyPrefersTheReply pins the race behind the CI flake in
// TestWorkerReportStatusCompletes: the loop answers, the answer ends the
// instance, and loopDone closes before the caller wakes. With both channels
// ready, the answer must win every time; a plain select picks either at random,
// so this fails about half of the iterations without the fix.
func TestAwaitReplyPrefersTheReply(t *testing.T) {
	for range 1000 {
		reply := make(chan int, 1)
		loopDone := make(chan struct{})
		reply <- 42
		close(loopDone)

		r, stopped, err := awaitReply(t.Context(), reply, loopDone)
		require.NoError(t, err)
		require.False(t, stopped)
		require.Equal(t, 42, r)
	}
}

// TestAwaitReplyWithoutAnAnswer covers the two ways a wait ends with no answer:
// the loop stopped, or the caller's context ended.
func TestAwaitReplyWithoutAnAnswer(t *testing.T) {
	t.Run("loop stopped", func(t *testing.T) {
		loopDone := make(chan struct{})
		close(loopDone)

		r, stopped, err := awaitReply(t.Context(), make(chan int, 1), loopDone)
		require.NoError(t, err)
		require.True(t, stopped)
		require.Zero(t, r)
	})

	t.Run("context canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, stopped, err := awaitReply(ctx, make(chan int, 1), make(chan struct{}))
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, stopped)
	})
}

// TestAdHocViewStoppedNamesTheInstance: when the loop accepts the view request
// and stops without answering, the error names the instance. The format had a
// %q with no argument, which printed %!q(MISSING).
func TestAdHocViewStoppedNamesTheInstance(t *testing.T) {
	var count atomic.Int32

	sl, err := activities.NewStandardLoop(loopCondLt(t, 3))
	require.NoError(t, err)

	inst := loopedSubProcessInstance(t, &count, sl)
	go func() { <-inst.taskReq; close(inst.loopDone) }() // accept, never reply

	_, _, err = inst.AdHocView(t.Context(), "body")
	require.ErrorContains(t, err,
		"instance \""+inst.ID()+"\" stopped before the ad-hoc view")
}

// TestRoundtripsAfterAccept: once the loop has taken a request, each caller
// reports a stop as its own InvalidState error and a canceled context as the
// context's error.
func TestRoundtripsAfterAccept(t *testing.T) {
	calls := map[string]struct {
		accept func(*Instance)
		call   func(context.Context, *Instance) error
	}{
		"job completion": {
			accept: func(inst *Instance) { <-inst.jobReq },
			call: func(ctx context.Context, inst *Instance) error {
				return inst.ReportJobCompletion(ctx,
					tasks.NewWorkerStatus("j", values.NewVariable("s")))
			},
		},
		"task roundtrip": {
			accept: func(inst *Instance) { <-inst.taskReq },
			call: func(ctx context.Context, inst *Instance) error {
				_, err := inst.taskRoundtrip(ctx, taskRequest{kind: reqTake})

				return err
			},
		},
		"ad-hoc view": {
			accept: func(inst *Instance) { <-inst.taskReq },
			call: func(ctx context.Context, inst *Instance) error {
				_, _, err := inst.AdHocView(ctx, "body")

				return err
			},
		},
	}

	for name, c := range calls {
		t.Run(name+"/loop stopped", func(t *testing.T) {
			var count atomic.Int32

			sl, err := activities.NewStandardLoop(loopCondLt(t, 3))
			require.NoError(t, err)

			inst := loopedSubProcessInstance(t, &count, sl)
			go func() { c.accept(inst); close(inst.loopDone) }()

			require.ErrorContains(t, c.call(t.Context(), inst), "stopped before")
		})

		t.Run(name+"/context canceled", func(t *testing.T) {
			var count atomic.Int32

			sl, err := activities.NewStandardLoop(loopCondLt(t, 3))
			require.NoError(t, err)

			inst := loopedSubProcessInstance(t, &count, sl)
			ctx, cancel := context.WithCancel(context.Background())
			go func() { c.accept(inst); cancel() }()

			require.ErrorIs(t, c.call(ctx, inst), context.Canceled)
		})
	}
}
