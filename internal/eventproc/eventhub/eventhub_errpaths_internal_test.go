package eventhub

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestPropagateEventProcessError covers EventHub.PropagateEvent's
// waiter-failure branch: a non-signal event with a registered waiter whose
// Process fails yields a wrapped "processing failed" error.
func TestPropagateEventProcessError(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	// a non-signal definition (terminate) is routed to eh.waiters[id].Process
	def, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	w := mockeventproc.NewMockEventWaiter(t)
	w.EXPECT().Process(mock.Anything).Return(errors.New("process boom"))
	w.EXPECT().ID().Return("mock-waiter")

	hub.m.Lock()
	hub.waiters[def.ID()] = w
	hub.m.Unlock()

	require.Error(t, hub.PropagateEvent(context.Background(), def))
}

// TestRegisterEventAddProcessorError covers EventHub.RegisterEvent's
// add-processor failure branch: when a waiter already exists for the event and
// AddEventProcessor fails, the error is wrapped and returned.
func TestRegisterEventAddProcessorError(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	def, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().ID().Return("ep-1").Maybe()

	w := mockeventproc.NewMockEventWaiter(t)
	w.EXPECT().AddEventProcessor(ep).Return(errors.New("add boom"))
	w.EXPECT().ID().Return("w-1").Maybe()

	hub.m.Lock()
	hub.waiters[def.ID()] = w
	hub.m.Unlock()

	require.Error(t, hub.RegisterEvent(ep, def))
}

// TestSignalCatchersFallbackCount (FIX-021): a signalIdx entry that does not
// expose ProcessorCount (not the concrete signalWaiter) still counts as one
// catcher — the defensive fallback of the readiness probe.
func TestSignalCatchersFallbackCount(t *testing.T) {
	eh, err := New(enginert.Default())
	require.NoError(t, err)

	// a bare mock EventWaiter has no ProcessorCount → the fallback branch.
	eh.signalIdx["GO"] = []eventproc.EventWaiter{
		mockeventproc.NewMockEventWaiter(t),
	}

	require.Equal(t, 1, eh.SignalCatchers("GO"))
}

// TestBroadcastSignalProcessError covers broadcastSignal's defensive branch
// (FIX-022 A1): a signalIdx waiter whose Process returns an error is logged and
// the broadcast continues (best-effort — it must reach every catcher, FIX-007),
// so broadcastSignal itself still returns nil.
func TestBroadcastSignalProcessError(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	sig, err := events.NewSignal("GO", nil)
	require.NoError(t, err)
	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	w := mockeventproc.NewMockEventWaiter(t)
	w.EXPECT().Process(mock.Anything).Return(errors.New("process boom"))
	w.EXPECT().ID().Return("mock-signal-waiter")

	hub.m.Lock()
	hub.signalIdx["GO"] = []eventproc.EventWaiter{w}
	hub.m.Unlock()

	require.NoError(t, hub.broadcastSignal(def),
		"a per-waiter Process error is logged, not propagated")
}
