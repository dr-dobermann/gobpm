package eventhub_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/stretchr/testify/require"
)

// createTimerExpressions создает FormalExpression для timer cycle и duration
func createTimerExpressions(t *testing.T) (cycle, duration data.FormalExpression) {
	// Создаем DataSource (можем использовать nil для простых тестов)
	var ds data.Source = nil

	// Создаем cycle expression (int - количество повторений)
	cycleValue := values.NewVariable(1) // 1 повторение
	cycleItemDef := data.MustItemDefinition(cycleValue)

	cycleExpr, err := goexpr.New(
		ds,
		cycleItemDef,
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			return cycleValue, nil
		},
	)
	require.NoError(t, err)

	// Создаем duration expression (time.Duration)
	durationValue := values.NewVariable(time.Second * 5) // 5 секунд
	durationItemDef := data.MustItemDefinition(durationValue)

	durationExpr, err := goexpr.New(
		ds,
		durationItemDef,
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			return durationValue, nil
		},
	)
	require.NoError(t, err)

	return cycleExpr, durationExpr
}

func TestTimerEvents(t *testing.T) {
	t.Run("successful timer event registration", func(t *testing.T) {
		hub, err := eventhub.New()
		require.NoError(t, err)

		// Start the hub
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			_ = hub.Run(ctx)
		}()
		time.Sleep(10 * time.Millisecond)

		mockProcessor := mockeventproc.NewMockEventProcessor(t)
		mockProcessor.EXPECT().Id().Return("timer-processor-id")

		// Create a timer event definition (supported by waiters)
		cycleExpr, durationExpr := createTimerExpressions(t)
		timerEvent, err := events.NewTimerEventDefinition(nil, cycleExpr, durationExpr)
		require.NoError(t, err)

		err = hub.RegisterEvent(mockProcessor, timerEvent)
		require.NoError(t, err)
	})

	t.Run("duplicate timer event registration", func(t *testing.T) {
		hub, err := eventhub.New()
		require.NoError(t, err)

		// Start the hub
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			_ = hub.Run(ctx)
		}()
		time.Sleep(10 * time.Millisecond)

		mockProcessor := mockeventproc.NewMockEventProcessor(t)
		mockProcessor.EXPECT().Id().Return("timer-processor-id").Maybe()

		// Create a timer event definition
		cycleExpr, durationExpr := createTimerExpressions(t)
		timerEvent, err := events.NewTimerEventDefinition(nil, cycleExpr, durationExpr)
		require.NoError(t, err)

		// First registration should succeed
		err = hub.RegisterEvent(mockProcessor, timerEvent)
		require.NoError(t, err)

		// Second registration should fail
		err = hub.RegisterEvent(mockProcessor, timerEvent)
		require.Error(t, err)
		require.Contains(t, err.Error(), "alredy registered")
	})

	t.Run("timer event unregistration", func(t *testing.T) {
		hub, err := eventhub.New()
		require.NoError(t, err)

		// Start the hub
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			_ = hub.Run(ctx)
		}()
		time.Sleep(10 * time.Millisecond)

		mockProcessor := mockeventproc.NewMockEventProcessor(t)
		processorId := "timer-processor-id"
		mockProcessor.EXPECT().Id().Return(processorId).Maybe()

		// Create and register a timer event
		cycleExpr, durationExpr := createTimerExpressions(t)
		timerEvent, err := events.NewTimerEventDefinition(nil, cycleExpr, durationExpr)
		require.NoError(t, err)

		err = hub.RegisterEvent(mockProcessor, timerEvent)
		require.NoError(t, err)

		// Try to unregister a different event ID
		err = hub.UnregisterEvent(mockProcessor, "non-existent-event-id")
		require.Error(t, err)
		require.Contains(t, err.Error(), "no waiter registered for eventDefiniton")

		// Unregister the correct event should work
		// Note: This will require mocking the waiter.Stop() method
		// For now, we'll just test the error case above
	})

	t.Run("timer event propagation to empty hub", func(t *testing.T) {
		hub, err := eventhub.New()
		require.NoError(t, err)

		// Start the hub
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			_ = hub.Run(ctx)
		}()
		time.Sleep(10 * time.Millisecond)

		// Create a timer event definition
		cycleExpr, durationExpr := createTimerExpressions(t)
		timerEvent, err := events.NewTimerEventDefinition(nil, cycleExpr, durationExpr)
		require.NoError(t, err)

		// Try to propagate event when no processors are registered
		err = hub.PropagateEvent(context.Background(), timerEvent)
		require.Error(t, err)
		require.Contains(t, err.Error(), "waiter isn't found")
	})
}

// TODO: Add test for successful timer event propagation when we have
// proper waiter mocking or integration test setup
