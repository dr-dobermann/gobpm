package instance

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/stretchr/testify/require"
)

func TestTokenStateProjection(t *testing.T) {
	cases := []struct {
		in   trackState
		want TokenState
	}{
		{TrackReady, TokenAlive},
		{TrackExecutingStep, TokenAlive},
		{TrackProcessStepResults, TokenAlive},
		{TrackWaitForEvent, TokenWaitForEvent},
		{TrackEnded, TokenConsumed},
		{TrackMerged, TokenConsumed},
		{TrackCanceled, TokenConsumed},
		{TrackFailed, TokenConsumed},
	}

	for _, c := range cases {
		require.Equalf(t, c.want, tokenStateFor(c.in),
			"projection of %s", c.in)
	}
}

// TestM3LinearHistory runs a linear instance with a fixed injected clock and
// checks the derived token path history: it visits both nodes, ends Consumed,
// has deterministic monotonic timestamps, and no active tokens remain.
func TestM3LinearHistory(t *testing.T) {
	_ = data.CreateDefaultStates()

	s := buildLinearSnapshot(t)
	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := New(s, nil, ep, nil)
	require.NoError(t, err)

	base := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	inst.now = newFakeClock(base).Now // fixed clock -> deterministic timestamps

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, inst.Run(ctx))
	require.Eventually(t,
		func() bool { return inst.State() == Finished },
		2*time.Second, 5*time.Millisecond)

	// all tokens consumed -> none active
	require.Empty(t, inst.GetTokens())

	hist := inst.TokenHistory()
	require.Len(t, hist, 1, "one linear track -> one path")

	p := hist[0]
	require.Equal(t, "", p.ParentID, "root track has no parent")
	require.Equal(t, TokenConsumed, p.Terminal)
	require.NotEmpty(t, p.Steps)

	nodes := map[string]bool{}

	var prev time.Time

	for _, sv := range p.Steps {
		nodes[sv.Node.Name()] = true

		require.Equal(t, base, sv.At, "fixed clock -> all timestamps equal")
		require.False(t, sv.At.Before(prev), "timestamps monotonic non-decreasing")

		prev = sv.At
	}

	require.True(t, nodes["start"], "path visits the start node")
	require.True(t, nodes["end"], "path visits the end node")
}

// TestM3ConcurrentReads hammers GetTokens / TokenHistory from another goroutine
// while the instance runs — under -race this proves the projections are
// lock-free and safe against the loop mutating the tracks snapshot.
func TestM3ConcurrentReads(t *testing.T) {
	_ = data.CreateDefaultStates()

	s := buildLinearSnapshot(t)
	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := New(s, nil, ep, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-done:
				return
			default:
				_ = inst.GetTokens()
				_ = inst.TokenHistory()
			}
		}
	}()

	require.NoError(t, inst.Run(ctx))
	require.Eventually(t,
		func() bool { return inst.State() == Finished },
		2*time.Second, 5*time.Millisecond)

	close(done)
}
