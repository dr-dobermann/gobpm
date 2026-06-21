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

// TestEventFlowLogging verifies the hub emits a Debug line at each matter point
// of the event path — register → waiter service → broadcast → deliver — when the
// configured logger is at Debug level (off at the default level, so normal runs
// stay quiet). This is the observability a developer turns on to watch the whole
// subscribe→fire→deliver flow.
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
		"event registered (new waiter)",
		"signal waiter serviced",
		"signal broadcast",
		"signal waiter delivering",
	} {
		require.Contains(t, out, want)
	}
}

// TestEventLifecycleLogging covers the remaining matter-point Debug lines: a
// second processor added to an existing waiter, both unregister branches
// (processor removed but waiter kept, then the last removal stopping the
// waiter), and propagation to no registered waiter (a logged no-op).
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
		"event registered (added to existing waiter)",
		"event propagated with no registered waiter",
		"event unregistered (processor removed, waiter kept)",
		"event unregistered (waiter stopped)",
	} {
		require.Contains(t, out, want)
	}
}
