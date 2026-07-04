package eventhub

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
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
