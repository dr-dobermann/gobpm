package instance_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/stretchr/testify/require"
)

// TestTokenStateString covers the projected token-state names (SRD-018).
func TestTokenStateString(t *testing.T) {
	for ts, want := range map[instance.TokenState]string{
		instance.TokenAlive:        "Alive",
		instance.TokenWaitForEvent: "WaitForEvent",
		instance.TokenConsumed:     "Consumed",
		instance.TokenWithdrawn:    "Withdrawn",
		instance.TokenInvalid:      "Invalid",
	} {
		require.Equal(t, want, ts.String())
	}
}

// TestObsKindString covers the observation-kind names, including the default.
func TestObsKindString(t *testing.T) {
	require.Equal(t, "InstanceState", instance.ObsInstanceState.String())
	require.Equal(t, "NodeProgress", instance.ObsNodeProgress.String())
	require.Equal(t, "Unknown", instance.ObsKind(9).String())
}

// TestInstanceObservers covers AddObserver (nil + real), the notify fan-out
// (via a real run), and removeObserver (cancel, plus a second cancel hitting the
// not-found path) within the instance package — they are otherwise exercised
// only cross-package through the thresher handle.
func TestInstanceObservers(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	s, err := getSnapshot("observers")
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := instance.New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	// A nil sink is ignored and yields a no-op cancel.
	inst.AddObserver(nil)()

	var (
		mu  sync.Mutex
		got []instance.ObsEvent
	)

	cancel := inst.AddObserver(func(ev instance.ObsEvent) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	})

	ctx, cc := context.WithCancel(context.Background())
	defer cc()
	require.NoError(t, inst.Run(ctx))

	select {
	case <-inst.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("instance did not finish")
	}

	mu.Lock()
	n := len(got)
	mu.Unlock()
	require.Positive(t, n, "the observer should receive events")

	// First cancel removes the sink; the second hits removeObserver's
	// not-found path.
	cancel()
	cancel()
}
