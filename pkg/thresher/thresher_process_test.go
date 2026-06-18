package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

func TestThresher_ProcessManagement(t *testing.T) {
	t.Run("register process with nil process", func(t *testing.T) {
		th, err := thresher.New("test-thresher")
		require.NoError(t, err)

		err = th.RegisterProcess(nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty process")
	})

	t.Run("register process success", func(t *testing.T) {
		th, err := thresher.New("test-thresher")
		require.NoError(t, err)

		proc, err := process.New("dummy process")
		require.NoError(t, err)

		err = th.RegisterProcess(proc)
		require.NoError(t, err)
	})

	t.Run("start process when thresher not started", func(t *testing.T) {
		th, err := thresher.New("test-thresher")
		require.NoError(t, err)
		require.Equal(t, thresher.NotStarted, th.State())

		_, err = th.StartProcess("some-process-id")
		require.Error(t, err)
		require.Contains(t, err.Error(), "thresher isn't started")
	})

	t.Run("start non-existent process", func(t *testing.T) {
		th, err := thresher.New("test-thresher")
		require.NoError(t, err)

		// Start thresher first
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err = th.Run(ctx)
		require.NoError(t, err)
		require.Equal(t, thresher.Started, th.State())

		_, err = th.StartProcess("non-existent-process")
		require.Error(t, err)
		require.Contains(t, err.Error(), "couldn't find snapshot for process ID")
	})

	// Note: Testing actual process start would require complex setup with
	// proper Process, Nodes, Flows, etc. We'll focus on error paths and
	// basic validation for now.
}

// TestStartProcess_NoReentrantDeadlock guards FIX-002 RC2: StartProcess must
// not hold t.m across launchInstance (which re-acquires it), or it self-
// deadlocks on the non-reentrant mutex.
func TestStartProcess_NoReentrantDeadlock(t *testing.T) {
	th, err := thresher.New("deadlock-test")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	// Minimal runnable process: Start -> End.
	proc, err := process.New("p")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	require.NoError(t, proc.Add(start))
	require.NoError(t, proc.Add(end))

	_, err = flow.Link(start, end)
	require.NoError(t, err)

	require.NoError(t, th.RegisterProcess(proc))

	done := make(chan error, 1)
	go func() { _, e := th.StartProcess(proc.ID()); done <- e }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("StartProcess deadlocked (FIX-002 RC2: re-entrant t.m)")
	}
}
