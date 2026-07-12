package eventhub_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestEventFlowLogging verifies the hub reports EventFlow facts at each matter
// point of the event path — register → deliver — echoed at Debug (off at the
// default level, so normal runs stay quiet), alongside the waiter-level Debug
// diagnostics. This is the observability a developer turns on to watch the whole
// subscribe→fire→deliver flow (SRD-041 §3.4).
func TestEventFlowLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf,
		&slog.HandlerOptions{Level: slog.LevelDebug}))

	hub, err := eventhub.New(enginert.Default().WithLogger(logger))
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().ID().Return("catcher").Maybe()
	ep.EXPECT().ProcessEvent(mock.Anything, mock.Anything).Return(nil).Once()

	require.NoError(t, hub.RegisterEvent(ep, signalDef(t, "GO")))
	require.NoError(t,
		hub.PropagateEvent(context.Background(), signalDef(t, "GO")))

	out := buf.String()
	for _, want := range []string{
		"EventFlow Registered",     // the new waiter registered
		"signal waiter serviced",   // waiter-level diagnostic (kept a log)
		"EventFlow Delivered",      // the signal reached its catcher(s)
		"signal waiter delivering", // waiter-level diagnostic (kept a log)
	} {
		require.Contains(t, out, want)
	}
}

// TestEventLifecycleLogging covers the remaining EventFlow matter points: a
// second processor added to an existing waiter (Registered), both unregister
// branches (Unregistered), and propagation to no registered waiter (Dropped).
func TestEventLifecycleLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf,
		&slog.HandlerOptions{Level: slog.LevelDebug}))

	hub, err := eventhub.New(enginert.Default().WithLogger(logger))
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	def := signalDef(t, "GO")

	ep1 := mockeventproc.NewMockEventProcessor(t)
	ep1.EXPECT().ID().Return("ep1").Maybe()
	ep2 := mockeventproc.NewMockEventProcessor(t)
	ep2.EXPECT().ID().Return("ep2").Maybe()

	require.NoError(t, hub.RegisterEvent(ep1, def)) // new waiter
	require.NoError(t, hub.RegisterEvent(ep2, def)) // added to existing waiter

	// A message with no registered waiter: a logged no-op, not an error.
	require.NoError(t,
		hub.PropagateEvent(context.Background(), msgEDef(t, "NOPE")))

	// First removal leaves the waiter (ep2 still on it); the second stops it.
	require.NoError(t, hub.UnregisterEvent(ep1, def.ID()))
	require.NoError(t, hub.UnregisterEvent(ep2, def.ID()))

	out := buf.String()
	for _, want := range []string{
		"EventFlow Registered",   // ep2 added to the existing waiter
		"EventFlow Dropped",      // propagated to no registered waiter
		"EventFlow Unregistered", // both unregister branches report it
	} {
		require.Contains(t, out, want)
	}
}
