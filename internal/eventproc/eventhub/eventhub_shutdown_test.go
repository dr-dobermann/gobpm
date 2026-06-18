package eventhub_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/stretchr/testify/require"
)

// TestEventHubShutdownDrainsWaiters registers a real timer waiter (a running
// service goroutine) and verifies Shutdown drains it — returns only after the
// goroutine exits (no leak; meaningful under -race) — then rejects further
// registration and is idempotent (SRD-019 FR-6, ADR-006 §2.5).
func TestEventHubShutdownDrainsWaiters(t *testing.T) {
	hub, err := eventhub.New(enginert.Default())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, hub.Start(ctx))

	mp := mockeventproc.NewMockEventProcessor(t)
	mp.EXPECT().ID().Return("ep").Maybe()

	// A 5s timer — it will not fire during the test; Shutdown's Stop unblocks the
	// service goroutine well before that.
	cycleExpr, durationExpr := createTimerExpressions(t)
	timerEvent, err := events.NewTimerEventDefinition(nil, cycleExpr, durationExpr)
	require.NoError(t, err)
	require.NoError(t, hub.RegisterEvent(mp, timerEvent))

	sctx, scancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer scancel()
	require.NoError(t, hub.Shutdown(sctx))

	// Stopped: further registration is rejected; Shutdown is idempotent.
	require.Error(t, hub.RegisterEvent(mp, timerEvent))
	require.NoError(t, hub.Shutdown(sctx))
}
