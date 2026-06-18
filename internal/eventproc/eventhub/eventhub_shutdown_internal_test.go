package eventhub

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/stretchr/testify/require"
)

// TestShutdownRemovesOnStopError verifies a waiter whose Stop errors is still
// removed from the registry — a failed Stop never leaks the entry (FR-6, NFR-2).
func TestShutdownRemovesOnStopError(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)

	dc := make(chan struct{})
	close(dc) // its service goroutine has already exited

	w := mockeventproc.NewMockEventWaiter(t)
	w.EXPECT().ID().Return("x").Maybe()
	w.EXPECT().Stop().Return(errors.New("stop failed"))
	w.EXPECT().Done().Return((<-chan struct{})(dc))

	hub.m.Lock()
	hub.waiters["x"] = w
	hub.m.Unlock()

	require.NoError(t, hub.Shutdown(context.Background()))

	hub.m.RLock()
	_, ok := hub.waiters["x"]
	hub.m.RUnlock()
	require.False(t, ok, "waiter must be removed even though its Stop errored")
}

// TestShutdownCtxBounded verifies Shutdown honours its ctx deadline when a
// waiter's service goroutine does not exit (its Done never closes) — it returns
// an error rather than hanging (FR-6, NFR-3).
func TestShutdownCtxBounded(t *testing.T) {
	hub, err := New(enginert.Default())
	require.NoError(t, err)

	dc := make(chan struct{}) // never closed — the goroutine "won't exit"
	defer close(dc)

	w := mockeventproc.NewMockEventWaiter(t)
	w.EXPECT().ID().Return("y").Maybe()
	w.EXPECT().Stop().Return(nil)
	w.EXPECT().Done().Return((<-chan struct{})(dc))

	hub.m.Lock()
	hub.waiters["y"] = w
	hub.m.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	require.Error(t, hub.Shutdown(ctx))
}
