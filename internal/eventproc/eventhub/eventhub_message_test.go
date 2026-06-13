package eventhub_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/stretchr/testify/require"
)

// TestMessageEvents тестирует поддержку Message событий
// В настоящее время waiters не поддерживает Message события,
// поэтому эти тесты демонстрируют текущие ограничения
func TestMessageEvents_CurrentLimitations(t *testing.T) {
	t.Run("message event registration fails - waiter not implemented", func(t *testing.T) {
		hub, err := eventhub.New(enginert.Default())
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, hub.Start(ctx))

		mockProcessor := mockeventproc.NewMockEventProcessor(t)
		mockProcessor.EXPECT().ID().Return("message-processor-id")

		// Create a message event definition
		message := bpmncommon.MustMessage("test-message", data.MustItemDefinition(nil))
		messageEvent, err := events.NewMessageEventDefinition(message, nil)
		require.NoError(t, err)

		// Registration should fail because waiters doesn't support Message events yet
		err = hub.RegisterEvent(mockProcessor, messageEvent)
		require.Error(t, err)

		// RegisterEvent wraps the builder failure in its own classified
		// error (FIX-003 §3.2.1) — the caller sees BUILDING_FAILED, and the
		// inner WAITERS_ERROR/OBJECT_NOT_FOUND chain (the unsupported
		// trigger) is preserved in the message.
		var ae *errs.ApplicationError

		require.True(t, errors.As(err, &ae))
		require.True(t, ae.HasClass(errs.BulidingFailed),
			"registration failure must be classified BUILDING_FAILED")
		require.Contains(t, err.Error(), errs.ObjectNotFound,
			"inner builder error preserved (no matching trigger)")
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
