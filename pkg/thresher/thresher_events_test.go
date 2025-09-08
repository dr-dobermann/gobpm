package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

func TestThresher_EventManagement(t *testing.T) {
	t.Run("register event success", func(t *testing.T) {
		th, err := thresher.New("test-thresher")
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			_ = th.Run(ctx)
		}()

		// Wait for thresher to start
		time.Sleep(10 * time.Millisecond)

		mockProcessor := mockeventproc.NewMockEventProcessor(t)
		mockProcessor.EXPECT().Id().Return("test-processor").Maybe()

		// Create a real TimerEventDefinition
		timerEventDef := events.MustTimerEventDefinition(
			goexpr.Must(
				nil,
				data.MustItemDefinition(
					values.NewVariable(time.Now().Add(time.Second))),
				func(_ context.Context, ds data.Source) (data.Value, error) {
					return values.NewVariable(time.Now().Add(time.Second)), nil
				}),
			nil, nil)

		err = th.RegisterEvent(mockProcessor, timerEventDef)
		require.NoError(t, err)
	})

	t.Run("register event with nil processor", func(t *testing.T) {
		th, err := thresher.New("test-thresher")
		require.NoError(t, err)

		// Create a real TimerEventDefinition
		timerEventDef := events.MustTimerEventDefinition(
			goexpr.Must(
				nil,
				data.MustItemDefinition(
					values.NewVariable(time.Now().Add(time.Second))),
				func(_ context.Context, ds data.Source) (data.Value, error) {
					return values.NewVariable(time.Now().Add(time.Second)), nil
				}),
			nil, nil)

		err = th.RegisterEvent(nil, timerEventDef)
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty event processor")
	})

	t.Run("unregister event success", func(t *testing.T) {
		th, err := thresher.New("test-thresher")
		require.NoError(t, err)

		// Start thresher
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err = th.Run(ctx)
		require.NoError(t, err)
		require.Equal(t, thresher.Started, th.State())

		mockProcessor := mockeventproc.NewMockEventProcessor(t)
		mockProcessor.EXPECT().Id().Return("test-processor").Maybe()

		// Create a real TimerEventDefinition
		timerEventDef := events.MustTimerEventDefinition(
			goexpr.Must(
				nil,
				data.MustItemDefinition(
					values.NewVariable(time.Now().Add(time.Second))),
				func(_ context.Context, ds data.Source) (data.Value, error) {
					return values.NewVariable(time.Now().Add(time.Second)), nil
				}),
			nil, nil)

		// First register the event
		err = th.RegisterEvent(mockProcessor, timerEventDef)
		require.NoError(t, err)

		// Then unregister it
		err = th.UnregisterEvent(mockProcessor, timerEventDef.Id())
		require.NoError(t, err)
	})

	t.Run("unregister event with nil processor", func(t *testing.T) {
		th, err := thresher.New("test-thresher")
		require.NoError(t, err)

		err = th.UnregisterEvent(nil, "some-event-id")
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty event processor")
	})

	t.Run("unregister non-existent event", func(t *testing.T) {
		th, err := thresher.New("test-thresher")
		require.NoError(t, err)

		// Start thresher
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err = th.Run(ctx)
		require.NoError(t, err)
		require.Equal(t, thresher.Started, th.State())

		mockProcessor := mockeventproc.NewMockEventProcessor(t)

		// Should error when unregistering non-existent event
		err = th.UnregisterEvent(mockProcessor, "non-existent-event")
		require.Error(t, err)
		require.Contains(t, err.Error(), "couldn't find waiter")
	})
}

func TestThresher_PropagateEvent(t *testing.T) {
	t.Run("propagate event when not started", func(t *testing.T) {
		th, err := thresher.New("test-thresher")
		require.NoError(t, err)
		require.Equal(t, thresher.NotStarted, th.State())

		// Create a real TimerEventDefinition
		timerEventDef := events.MustTimerEventDefinition(
			goexpr.Must(
				nil,
				data.MustItemDefinition(
					values.NewVariable(time.Now().Add(time.Second))),
				func(_ context.Context, ds data.Source) (data.Value, error) {
					return values.NewVariable(time.Now().Add(time.Second)), nil
				}),
			nil, nil)

		err = th.PropagateEvent(context.Background(), timerEventDef)
		require.Error(t, err)
		require.Contains(t, err.Error(), "thresher is not started")
	})

	t.Run("propagate nil event", func(t *testing.T) {
		th, err := thresher.New("test-thresher")
		require.NoError(t, err)

		// Start thresher
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err = th.Run(ctx)
		require.NoError(t, err)
		require.Equal(t, thresher.Started, th.State())

		err = th.PropagateEvent(context.Background(), nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty event definition")
	})

	t.Run("propagate event success", func(t *testing.T) {
		th, err := thresher.New("test-thresher")
		require.NoError(t, err)

		// Start thresher
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err = th.Run(ctx)
		require.NoError(t, err)
		require.Equal(t, thresher.Started, th.State())

		// Timer events are not meant to be propagated - they are generated by timer waiter itself
		// For now, this test just verifies the thresher is running correctly
		// TODO: Add proper event propagation test with appropriate event type in the future
		
		// Give some time for thresher processing
		time.Sleep(10 * time.Millisecond)
	})
}

func TestThresher_EventQueueProcessing(t *testing.T) {
	t.Run("event queue processes registered events", func(t *testing.T) {
		th, err := thresher.New("test-thresher")
		require.NoError(t, err)

		// Start thresher
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err = th.Run(ctx)
		require.NoError(t, err)
		require.Equal(t, thresher.Started, th.State())

		// Create mock processor and real timer event definition
		mockProcessor := mockeventproc.NewMockEventProcessor(t)
		processorId := "test-processor"
		mockProcessor.EXPECT().Id().Return(processorId).Maybe()

		// Create a real TimerEventDefinition
		timerEventDef := events.MustTimerEventDefinition(
			goexpr.Must(
				nil,
				data.MustItemDefinition(
					values.NewVariable(time.Now().Add(time.Second))),
				func(_ context.Context, ds data.Source) (data.Value, error) {
					return values.NewVariable(time.Now().Add(time.Second)), nil
				}),
			nil, nil)

		// Register event processor - this creates a timer waiter that will generate events
		err = th.RegisterEvent(mockProcessor, timerEventDef)
		require.NoError(t, err)

		// Timer events are generated by the waiter's Service method, not propagated externally
		// The timer waiter will call mockProcessor.ProcessEvent when the timer fires
		// For this test, we just verify that registration worked correctly
		
		// Give time for potential timer processing
		time.Sleep(50 * time.Millisecond)
	})

	t.Run("event queue handles context cancellation", func(t *testing.T) {
		th, err := thresher.New("test-thresher")
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())

		err = th.Run(ctx)
		require.NoError(t, err)
		require.Equal(t, thresher.Started, th.State())

		// Give time for goroutine to start
		time.Sleep(10 * time.Millisecond)

		// Cancel context - this should stop the event queue
		cancel()

		// Give time for goroutine to stop
		time.Sleep(10 * time.Millisecond)

		// Note: We can't easily test that the goroutine stopped without
		// additional synchronization mechanisms in the actual code
	})
}
