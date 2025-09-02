package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/generated/mockflow"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

func TestThresher_EventManagement(t *testing.T) {
	t.Run("register event success", func(t *testing.T) {
		th := thresher.New()

		mockProcessor := mockeventproc.NewMockEventProcessor(t)
		mockProcessor.EXPECT().Id().Return("test-processor").Maybe()

		mockEventDef := mockflow.NewMockEventDefinition(t)
		mockEventDef.EXPECT().Id().Return("test-event-def").Maybe()

		err := th.RegisterEvent(mockProcessor, mockEventDef)
		require.NoError(t, err)
	})

	t.Run("register event with nil processor", func(t *testing.T) {
		th := thresher.New()

		mockEventDef := mockflow.NewMockEventDefinition(t)

		err := th.RegisterEvent(nil, mockEventDef)
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty event processor")
	})

	t.Run("unregister event success", func(t *testing.T) {
		th := thresher.New()

		mockProcessor := mockeventproc.NewMockEventProcessor(t)
		mockProcessor.EXPECT().Id().Return("test-processor").Maybe()

		mockEventDef := mockflow.NewMockEventDefinition(t)
		eventDefId := "test-event-def"
		mockEventDef.EXPECT().Id().Return(eventDefId).Maybe()

		// First register the event
		err := th.RegisterEvent(mockProcessor, mockEventDef)
		require.NoError(t, err)

		// Then unregister it
		err = th.UnregisterEvent(mockProcessor, eventDefId)
		require.NoError(t, err)
	})

	t.Run("unregister event with nil processor", func(t *testing.T) {
		th := thresher.New()

		err := th.UnregisterEvent(nil, "some-event-id")
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty event processor")
	})

	t.Run("unregister non-existent event", func(t *testing.T) {
		th := thresher.New()

		mockProcessor := mockeventproc.NewMockEventProcessor(t)

		// Should not error when unregistering non-existent event
		err := th.UnregisterEvent(mockProcessor, "non-existent-event")
		require.NoError(t, err)
	})
}

func TestThresher_PropagateEvent(t *testing.T) {
	t.Run("propagate event when not started", func(t *testing.T) {
		th := thresher.New()
		require.Equal(t, thresher.NotStarted, th.State())

		mockEventDef := mockflow.NewMockEventDefinition(t)

		err := th.PropagateEvent(context.Background(), mockEventDef)
		require.Error(t, err)
		require.Contains(t, err.Error(), "thresher isn't started")
	})

	t.Run("propagate nil event", func(t *testing.T) {
		th := thresher.New()

		// Start thresher
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err := th.Run(ctx)
		require.NoError(t, err)
		require.Equal(t, thresher.Started, th.State())

		err = th.PropagateEvent(context.Background(), nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty event definition")
	})

	t.Run("propagate event success", func(t *testing.T) {
		th := thresher.New()

		// Start thresher
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err := th.Run(ctx)
		require.NoError(t, err)
		require.Equal(t, thresher.Started, th.State())

		mockEventDef := mockflow.NewMockEventDefinition(t)
		mockEventDef.EXPECT().Id().Return("test-event-def").Maybe()

		err = th.PropagateEvent(context.Background(), mockEventDef)
		require.NoError(t, err)

		// Give some time for event processing
		time.Sleep(10 * time.Millisecond)
	})
}

func TestThresher_EventQueueProcessing(t *testing.T) {
	t.Run("event queue processes registered events", func(t *testing.T) {
		th := thresher.New()

		// Start thresher
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err := th.Run(ctx)
		require.NoError(t, err)
		require.Equal(t, thresher.Started, th.State())

		// Create mock processor and event
		mockProcessor := mockeventproc.NewMockEventProcessor(t)
		processorId := "test-processor"
		mockProcessor.EXPECT().Id().Return(processorId).Maybe()

		mockEventDef := mockflow.NewMockEventDefinition(t)
		eventDefId := "test-event-def"
		mockEventDef.EXPECT().Id().Return(eventDefId).Maybe()

		// Setup expectation for ProcessEvent call
		mockProcessor.EXPECT().ProcessEvent(ctx, mockEventDef).Return(nil).Maybe()

		// Register event processor
		err = th.RegisterEvent(mockProcessor, mockEventDef)
		require.NoError(t, err)

		// Propagate event to trigger processing
		err = th.PropagateEvent(ctx, mockEventDef)
		require.NoError(t, err)

		// Give time for async processing
		time.Sleep(50 * time.Millisecond)
	})

	t.Run("event queue handles context cancellation", func(t *testing.T) {
		th := thresher.New()

		ctx, cancel := context.WithCancel(context.Background())

		err := th.Run(ctx)
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
