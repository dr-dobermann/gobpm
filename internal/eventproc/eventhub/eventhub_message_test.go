package eventhub_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/stretchr/testify/require"
)

// TestMessageEvents тестирует поддержку Message событий
// В настоящее время waiters не поддерживает Message события,
// поэтому эти тесты демонстрируют текущие ограничения
func TestMessageEvents_CurrentLimitations(t *testing.T) {
	t.Run("message event registration fails - waiter not implemented", func(t *testing.T) {
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
		mockProcessor.EXPECT().ID().Return("message-processor-id")

		// Create a message event definition
		message := common.MustMessage("test-message", data.MustItemDefinition(nil))
		messageEvent, err := events.NewMessageEventDefintion(message, nil)
		require.NoError(t, err)

		// Registration should fail because waiters doesn't support Message events yet
		err = hub.RegisterEvent(mockProcessor, messageEvent)
		require.Error(t, err)
		require.Contains(t, err.Error(), "eventWaiter building failed")
		require.Contains(t, err.Error(), "couldn't find builder for eventDefintion")
		require.Contains(t, err.Error(), "of type Message")
	})
}

// TODO: После добавления поддержки Message событий в waiters package,
// добавить следующие тесты:
//
// func TestMessageEvents_WithWaiterSupport(t *testing.T) {
//     t.Run("successful message event registration", func(t *testing.T) {
//         // Test successful registration
//     })
//
//     t.Run("message event propagation", func(t *testing.T) {
//         // Test event propagation to registered processors
//     })
//
//     t.Run("message event unregistration", func(t *testing.T) {
//         // Test event unregistration
//     })
// }
