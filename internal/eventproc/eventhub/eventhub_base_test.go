package eventhub_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/generated/mockflow"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Run("successful creation", func(t *testing.T) {
		hub, err := eventhub.New()
		require.NoError(t, err)
		require.NotNil(t, hub)
	})
}

func TestStart(t *testing.T) {
	t.Run("successful start", func(t *testing.T) {
		hub, err := eventhub.New()
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		require.NoError(t, hub.Start(ctx))
	})

	t.Run("double start error", func(t *testing.T) {
		hub, err := eventhub.New()
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		require.NoError(t, hub.Start(ctx))

		err = hub.Start(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "eventHub is already started")
	})
}

func TestRun(t *testing.T) {
	t.Run("run before start error", func(t *testing.T) {
		hub, err := eventhub.New()
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err = hub.Run(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "eventHub isn't started")
	})

	t.Run("successful run with timeout", func(t *testing.T) {
		hub, err := eventhub.New()
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(),
			100*time.Millisecond)
		defer cancel()

		require.NoError(t, hub.Start(ctx))

		err = hub.Run(ctx)
		require.Error(t, err)
		require.Equal(t, context.DeadlineExceeded, err)
	})

	t.Run("successful run with cancellation", func(t *testing.T) {
		hub, err := eventhub.New()
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		require.NoError(t, hub.Start(ctx))

		err = hub.Run(ctx)
		require.Error(t, err)
		require.Equal(t, context.Canceled, err)
	})
}

func TestRegisterEvent_BaseErrors(t *testing.T) {
	t.Run("hub not started error", func(t *testing.T) {
		hub, err := eventhub.New()
		require.NoError(t, err)

		mockProcessor := mockeventproc.NewMockEventProcessor(t)

		err = hub.RegisterEvent(mockProcessor, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "eventHub isn't started")
	})

	t.Run("nil event processor error", func(t *testing.T) {
		hub, err := eventhub.New()
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, hub.Start(ctx))

		err = hub.RegisterEvent(nil, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty event processor isn't allowed")
	})
}

func TestUnregisterEvent_BaseErrors(t *testing.T) {
	t.Run("hub not started error", func(t *testing.T) {
		hub, err := eventhub.New()
		require.NoError(t, err)

		mockProcessor := mockeventproc.NewMockEventProcessor(t)

		err = hub.UnregisterEvent(mockProcessor, "some-event-id")
		require.Error(t, err)
		require.Contains(t, err.Error(), "eventHub isn't started")
	})

	t.Run("nil event processor error", func(t *testing.T) {
		hub, err := eventhub.New()
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, hub.Start(ctx))

		err = hub.UnregisterEvent(nil, "some-event-id")
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty event processor isn't allowed")
	})

	t.Run("processor not found error", func(t *testing.T) {
		hub, err := eventhub.New()
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, hub.Start(ctx))

		mockProcessor := mockeventproc.NewMockEventProcessor(t)

		err = hub.UnregisterEvent(mockProcessor, "some-event-id")
		require.Error(t, err)
		require.Contains(t, err.Error(), "couldn't find waiter for the event definition")
	})
}

func TestPropagateEvent_BaseErrors(t *testing.T) {
	t.Run("hub not started error", func(t *testing.T) {
		hub, err := eventhub.New()
		require.NoError(t, err)

		err = hub.PropagateEvent(context.Background(), nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "eventHub isn't started")
	})

	t.Run("no waiters found error", func(t *testing.T) {
		hub, err := eventhub.New()
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, hub.Start(ctx))

		mockEventDef := mockflow.NewMockEventDefinition(t)
		mockEventDef.EXPECT().ID().Return("test-event-id").Maybe()
		mockEventDef.EXPECT().Type().Return(flow.EventTrigger("TestType")).Maybe()

		err = hub.PropagateEvent(context.Background(), mockEventDef)
		require.Error(t, err)
		require.Contains(t, err.Error(), "couldn't find waiter for EventDefinition")
	})
}
