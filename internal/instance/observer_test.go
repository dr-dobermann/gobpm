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
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/stretchr/testify/require"
)

// TestTokenStateString covers the projected token-state names (SRD-018).
func TestTokenStateString(t *testing.T) {
	for ts, want := range map[instance.TokenState]string{
		instance.TokenAlive:        "Alive",
		instance.TokenWaitForEvent: "WaitForEvent",
		instance.TokenConsumed:     "Consumed",
		instance.TokenInvalid:      "Invalid",
	} {
		require.Equal(t, want, ts.String())
	}
}

// TestInstanceObservers covers AddObserver (nil + real), the observe fan-out
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
		got []observability.ObsEvent
	)

	cancel := inst.AddObserver(func(ev observability.ObsEvent) {
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
	defer mu.Unlock()
	require.Positive(t, len(got), "the observer should receive events")

	// observe() stamps every event with a timestamp and the instance id, and
	// emits the canonical kinds.
	for _, ev := range got {
		require.False(t, ev.At.IsZero(), "event timestamp must be stamped")
		require.Equal(t, inst.ID(), ev.Details[observability.AttrInstanceID],
			"instance_id must be stamped into details")
		require.Contains(t,
			[]observability.Kind{
				observability.KindInstanceState,
				observability.KindNodeProgress,
			}, ev.Kind)
	}

	// First cancel removes the sink; the second hits removeObserver's
	// not-found path.
	cancel()
	cancel()
}
