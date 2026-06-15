package eventhub_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/stretchr/testify/require"
)

// TestMessageEvents covers Message event support: since SRD-013 added the
// MessageWaiter (ADR-014 v.1), registering a message event now succeeds — the
// hub builds a MessageWaiter that subscribes the broker. Event propagation to
// the processor is covered by the waiters package (TestMessageWaiterDelivery)
// and, end to end through a node, by the ReceiveTask tests.
func TestMessageEvents(t *testing.T) {
	t.Run("message event registration succeeds with the MessageWaiter",
		func(t *testing.T) {
			hub, err := eventhub.New(enginert.Default())
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			require.NoError(t, hub.Start(ctx))

			mockProcessor := mockeventproc.NewMockEventProcessor(t)
			mockProcessor.EXPECT().ID().Return("message-processor-id").Maybe()

			message := bpmncommon.MustMessage("test-message",
				data.MustItemDefinition(nil))
			messageEvent, err := events.NewMessageEventDefinition(message, nil)
			require.NoError(t, err)

			require.NoError(t, hub.RegisterEvent(mockProcessor, messageEvent))
			require.NoError(t,
				hub.UnregisterEvent(mockProcessor, messageEvent.ID()))
		})
}
