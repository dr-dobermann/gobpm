package eventhub_test

import (
	"context"
	"github.com/dr-dobermann/gobpm/internal/enginert"
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
		hub, err := eventhub.New(enginert.Default())
		require.NoError(t, err)
		require.NotNil(t, hub)
	})

	t.Run("nil runtime rejected", func(t *testing.T) {
		_, err := eventhub.New(nil)
		require.Error(t, err)
	})
}

func TestStart(t *testing.T) {
	t.Run("successful start", func(t *testing.T) {
		hub, err := eventhub.New(enginert.Default())
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		require.NoError(t, hub.Start(ctx))
	})

	t.Run("double start error", func(t *testing.T) {
		hub, err := eventhub.New(enginert.Default())
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
		hub, err := eventhub.New(enginert.Default())
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err = hub.Run(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "eventHub isn't started")
	})

	t.Run("successful run with timeout", func(t *testing.T) {
		hub, err := eventhub.New(enginert.Default())
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
		hub, err := eventhub.New(enginert.Default())
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
		hub, err := eventhub.New(enginert.Default())
		require.NoError(t, err)

		mockProcessor := mockeventproc.NewMockEventProcessor(t)

		err = hub.RegisterEvent(mockProcessor, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "eventHub isn't started")
	})

	t.Run("nil event processor error", func(t *testing.T) {
		hub, err := eventhub.New(enginert.Default())
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
		hub, err := eventhub.New(enginert.Default())
		require.NoError(t, err)

		mockProcessor := mockeventproc.NewMockEventProcessor(t)

		err = hub.UnregisterEvent(mockProcessor, "some-event-id")
		require.Error(t, err)
		require.Contains(t, err.Error(), "eventHub isn't started")
	})

	t.Run("nil event processor error", func(t *testing.T) {
		hub, err := eventhub.New(enginert.Default())
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, hub.Start(ctx))

		err = hub.UnregisterEvent(nil, "some-event-id")
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty event processor isn't allowed")
	})

	t.Run("processor not found error", func(t *testing.T) {
		hub, err := eventhub.New(enginert.Default())
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
		hub, err := eventhub.New(enginert.Default())
		require.NoError(t, err)

		err = hub.PropagateEvent(context.Background(), nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "eventHub isn't started")
	})

	t.Run("no waiters found is a no-op (ADR-006 §2.4)", func(t *testing.T) {
		hub, err := eventhub.New(enginert.Default())
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, hub.Start(ctx))

		mockEventDef := mockflow.NewMockEventDefinition(t)
		mockEventDef.EXPECT().ID().Return("test-event-id").Maybe()
		mockEventDef.EXPECT().Type().Return(flow.EventTrigger("TestType")).Maybe()

		// Propagating to no registered waiter is a logged no-op, not an error.
		err = hub.PropagateEvent(context.Background(), mockEventDef)
		require.NoError(t, err)
	})
}

// TestPropagateNoWaiterIsNoop is the ADR-006 §2.4 regression (SRD-020):
// PropagateEvent to an absent key is a no-op returning nil for both a signal
// and a non-signal trigger — correct for a signal thrown into the void
// (BPMN §10.5.1), harmless for any other kind.
func TestPropagateNoWaiterIsNoop(t *testing.T) {
	for _, trig := range []flow.EventTrigger{
		flow.TriggerSignal, flow.TriggerMessage,
	} {
		t.Run(string(trig), func(t *testing.T) {
			hub, err := eventhub.New(enginert.Default())
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			require.NoError(t, hub.Start(ctx))

			eDef := mockflow.NewMockEventDefinition(t)
			eDef.EXPECT().ID().Return("absent-" + string(trig)).Maybe()
			eDef.EXPECT().Type().Return(trig).Maybe()

			require.NoError(t, hub.PropagateEvent(ctx, eDef))
		})
	}
}
