package thresher_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

func TestThresher_ProcessManagement(t *testing.T) {
	t.Run("register process with nil snapshot", func(t *testing.T) {
		th := thresher.New()

		err := th.RegisterProcess(nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty snapshot")
	})

	t.Run("register process success", func(t *testing.T) {
		th := thresher.New()

		// Create a minimal snapshot for testing
		snap := &snapshot.Snapshot{
			ID:          *foundation.NewID(),
			ProcessId:   "test-process-id",
			ProcessName: "Test Process",
			Nodes:       map[string]flow.Node{},
			Flows:       map[string]*flow.SequenceFlow{},
			Properties:  nil,
			InitEvents:  []flow.EventNode{},
		}

		err := th.RegisterProcess(snap)
		require.NoError(t, err)
	})

	t.Run("start process when thresher not started", func(t *testing.T) {
		th := thresher.New()
		require.Equal(t, thresher.NotStarted, th.State())

		err := th.StartProcess("some-process-id")
		require.Error(t, err)
		require.Contains(t, err.Error(), "thresher isn't started")
	})

	t.Run("start non-existent process", func(t *testing.T) {
		th := thresher.New()

		// Start thresher first
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err := th.Run(ctx)
		require.NoError(t, err)
		require.Equal(t, thresher.Started, th.State())

		err = th.StartProcess("non-existent-process")
		require.Error(t, err)
		require.Contains(t, err.Error(), "couldn't find snapshot for process ID")
	})

	// Note: Testing actual process start would require complex setup with
	// proper Process, Nodes, Flows, etc. We'll focus on error paths and
	// basic validation for now.
}
