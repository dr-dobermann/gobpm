package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

func TestState(t *testing.T) {
	t.Run("state validation", func(t *testing.T) {
		// Valid states
		require.NoError(t, thresher.NotStarted.Validate())
		require.NoError(t, thresher.Started.Validate())
		require.NoError(t, thresher.Paused.Validate())

		// Invalid state
		invalidState := thresher.State(99)
		require.Error(t, invalidState.Validate())
	})

	t.Run("state string representation", func(t *testing.T) {
		require.Equal(t, "NotStarted ", thresher.NotStarted.String())
		require.Equal(t, "Started", thresher.Started.String())
		require.Equal(t, "Paused", thresher.Paused.String())

		// Invalid state returns error message
		invalidState := thresher.State(99)
		require.Contains(t, invalidState.String(), "invalid thresher state")
	})
}

func TestThresher_StateManagement(t *testing.T) {
	t.Run("new thresher starts in NotStarted state", func(t *testing.T) {
		th := thresher.New()
		require.NotNil(t, th)
		require.Equal(t, thresher.NotStarted, th.State())
	})

	t.Run("update state success", func(t *testing.T) {
		th := thresher.New()

		err := th.UpdateState(thresher.Started)
		require.NoError(t, err)
		require.Equal(t, thresher.Started, th.State())

		err = th.UpdateState(thresher.Paused)
		require.NoError(t, err)
		require.Equal(t, thresher.Paused, th.State())
	})

	t.Run("update state with invalid state", func(t *testing.T) {
		th := thresher.New()

		invalidState := thresher.State(99)
		err := th.UpdateState(invalidState)
		require.Error(t, err)
		require.Contains(t, err.Error(), "couldn't set new state")
		require.Equal(t, thresher.NotStarted, th.State()) // Should remain unchanged
	})
}

func TestThresher_Run(t *testing.T) {
	t.Run("successful run", func(t *testing.T) {
		th := thresher.New()
		require.Equal(t, thresher.NotStarted, th.State())

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err := th.Run(ctx)
		require.NoError(t, err)
		require.Equal(t, thresher.Started, th.State())

		// Give some time for goroutine to start
		time.Sleep(10 * time.Millisecond)

		// Cancel context to stop the thresher
		cancel()
		time.Sleep(10 * time.Millisecond)
	})

	t.Run("run from invalid state", func(t *testing.T) {
		th := thresher.New()

		// First run should succeed
		ctx1, cancel1 := context.WithCancel(context.Background())
		defer cancel1()

		err := th.Run(ctx1)
		require.NoError(t, err)
		require.Equal(t, thresher.Started, th.State())

		// Second run should fail
		ctx2, cancel2 := context.WithCancel(context.Background())
		defer cancel2()

		err = th.Run(ctx2)
		require.Error(t, err)
		require.Contains(t, err.Error(), "couldn't start thresher from state")
	})

	t.Run("run with nil context", func(t *testing.T) {
		th := thresher.New()

		err := th.Run(nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty context")
		require.Equal(t, thresher.NotStarted, th.State()) // Should remain unchanged
	})

	t.Run("run and pause workflow", func(t *testing.T) {
		th := thresher.New()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start thresher
		err := th.Run(ctx)
		require.NoError(t, err)
		require.Equal(t, thresher.Started, th.State())

		// Pause thresher
		err = th.UpdateState(thresher.Paused)
		require.NoError(t, err)
		require.Equal(t, thresher.Paused, th.State())

		// Resume thresher
		err = th.UpdateState(thresher.Started)
		require.NoError(t, err)
		require.Equal(t, thresher.Started, th.State())
	})
}
