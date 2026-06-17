package eventhub_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/mock"
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

// TestAddEventKey covers the lazy-association extend seam (SRD-017 §4.5): an
// empty id is rejected, an unknown id is a benign no-op, and adding a key to a
// registered message receiver makes a message carrying that key deliver to it.
func TestAddEventKey(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rt := enginert.Default()
	hub, err := eventhub.New(rt)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, hub.Start(ctx))

	require.Error(t, hub.AddEventKey("", "K1"))         // empty id
	require.NoError(t, hub.AddEventKey("missing", "K1")) // unknown -> no-op

	eDef := msgEDef(t, "reply")

	done := make(chan struct{}, 1)
	mp := mockeventproc.NewMockEventProcessor(t)
	mp.EXPECT().ID().Return("p").Maybe()
	mp.EXPECT().
		ProcessEvent(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, flow.EventDefinition) error {
			done <- struct{}{}

			return nil
		})

	require.NoError(t, hub.RegisterEvent(mp, eDef))      // wildcard subscription
	require.NoError(t, hub.AddEventKey(eDef.ID(), "K1")) // now keyed {K1}

	require.NoError(t, rt.MessageBroker().Publish(ctx, messaging.Envelope{
		Name: "reply", CorrelationKey: "K1"}))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("keyed message after AddEventKey was not delivered")
	}
}

// msgEDef builds a message event definition for the persistent-registration
// tests.
func msgEDef(t *testing.T, name string) *events.MessageEventDefinition {
	t.Helper()

	med, err := events.NewMessageEventDefinition(
		bpmncommon.MustMessage(name, data.MustItemDefinition(nil)), nil)
	require.NoError(t, err)

	return med
}

// TestRegisterPersistentEvent covers the instance-starter registration path
// (SRD-015 M2): a message trigger registers a persistent waiter that the hub
// retains until UnregisterEvent; non-message triggers, an unstarted hub, and a
// nil processor are rejected.
func TestRegisterPersistentEvent(t *testing.T) {
	t.Run("message registration succeeds and tears down", func(t *testing.T) {
		hub, err := eventhub.New(enginert.Default())
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, hub.Start(ctx))

		ep := mockeventproc.NewMockEventProcessor(t)
		ep.EXPECT().ID().Return("starter").Maybe()

		med := msgEDef(t, "start-message")
		require.NoError(t, hub.RegisterPersistentEvent(ep, med))
		require.NoError(t, hub.UnregisterEvent(ep, med.ID()))
	})

	t.Run("non-message trigger rejected", func(t *testing.T) {
		hub, err := eventhub.New(enginert.Default())
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, hub.Start(ctx))

		ep := mockeventproc.NewMockEventProcessor(t)
		ep.EXPECT().ID().Return("starter").Maybe()

		require.Error(t, hub.RegisterPersistentEvent(ep,
			events.MustSignalEventDefinition(&events.Signal{})))
	})

	t.Run("unstarted hub rejected", func(t *testing.T) {
		hub, err := eventhub.New(enginert.Default())
		require.NoError(t, err)

		ep := mockeventproc.NewMockEventProcessor(t)
		require.Error(t, hub.RegisterPersistentEvent(ep, msgEDef(t, "m")))
	})

	t.Run("nil processor rejected", func(t *testing.T) {
		hub, err := eventhub.New(enginert.Default())
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, hub.Start(ctx))

		require.Error(t, hub.RegisterPersistentEvent(nil, msgEDef(t, "m")))
	})
}

// TestAddEventKeyNonMessageWaiter covers the non-keyable branch of AddEventKey:
// a timer (non-message) waiter has no keyed subscription, so AddEventKey is a
// benign no-op (SRD-017 §4.5).
func TestAddEventKeyNonMessageWaiter(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	hub, err := eventhub.New(enginert.Default())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, hub.Start(ctx))

	mp := mockeventproc.NewMockEventProcessor(t)
	mp.EXPECT().ID().Return("p").Maybe()
	mp.EXPECT().ProcessEvent(mock.Anything, mock.Anything).Return(nil).Maybe()

	cycle, dur := createTimerExpressions(t)
	timer, err := events.NewTimerEventDefinition(nil, cycle, dur)
	require.NoError(t, err)
	require.NoError(t, hub.RegisterEvent(mp, timer))

	require.NoError(t, hub.AddEventKey(timer.ID(), "K1"))
}
