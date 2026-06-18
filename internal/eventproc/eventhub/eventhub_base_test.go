package eventhub_test

import (
	"context"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/generated/mockflow"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
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
// PropagateEvent with no catcher is a no-op returning nil — for a signal thrown
// into the void (broadcast with zero subscribers, BPMN §10.5.1) and for a
// non-signal trigger with no registered waiter.
func TestPropagateNoWaiterIsNoop(t *testing.T) {
	startedHub := func(t *testing.T) *eventhub.EventHub {
		t.Helper()

		hub, err := eventhub.New(enginert.Default())
		require.NoError(t, err)
		require.NoError(t, hub.Start(context.Background()))

		return hub
	}

	t.Run("signal broadcast with no catcher", func(t *testing.T) {
		sig, err := events.NewSignal("GO", nil)
		require.NoError(t, err)
		def, err := events.NewSignalEventDefinition(sig)
		require.NoError(t, err)

		require.NoError(t, startedHub(t).PropagateEvent(context.Background(), def))
	})

	t.Run("non-signal with no waiter", func(t *testing.T) {
		eDef := mockflow.NewMockEventDefinition(t)
		eDef.EXPECT().ID().Return("absent").Maybe()
		eDef.EXPECT().Type().Return(flow.TriggerMessage).Maybe()

		require.NoError(t, startedHub(t).PropagateEvent(context.Background(), eDef))
	})

	// A TriggerSignal eDef that isn't a *events.SignalEventDefinition trips
	// broadcastSignal's defensive type guard (real signals always are).
	t.Run("signal-typed non-signal errors", func(t *testing.T) {
		eDef := mockflow.NewMockEventDefinition(t)
		eDef.EXPECT().ID().Return("bogus").Maybe()
		eDef.EXPECT().Type().Return(flow.TriggerSignal).Maybe()

		require.Error(t, startedHub(t).PropagateEvent(context.Background(), eDef))
	})
}
