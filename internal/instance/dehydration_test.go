package instance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
)

// TestTrackDehydratedReleasesGoroutine covers SRD-071 T-1: closing a parked
// track's dehydrateCh makes its run() return in TrackDehydrated (the goroutine
// exits), and that return classifies as evDehydrated.
func TestTrackDehydratedReleasesGoroutine(t *testing.T) {
	val := false
	evals := 0

	def, err := events.NewConditionalEventDefinition(condExpr(t, &val, &evals))
	require.NoError(t, err)

	_, tr, _ := condInstance(t, def)
	require.True(t, tr.inState(TrackWaitForEvent))

	done := make(chan struct{})
	go func() {
		tr.run(context.Background())
		close(done)
	}()

	// A closed channel is permanently ready, so the release is delivered
	// whether run() is already parked on the select or reaches it after.
	ls := newLoopState(tr.instance)
	ls.dehydrateTrack(tr)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not return after dehydrateTrack")
	}

	require.True(t, tr.inState(TrackDehydrated),
		"a released track ends in TrackDehydrated")
	require.Equal(t, evDehydrated, trackEndKind(tr),
		"a TrackDehydrated return classifies as evDehydrated")
}

// TestApplyDehydratedRetainsRecord covers SRD-071 FR-1: applyDehydrated
// decrements the active count but retains the track record and its bookkeeping.
func TestApplyDehydratedRetainsRecord(t *testing.T) {
	val := false
	evals := 0

	def, err := events.NewConditionalEventDefinition(condExpr(t, &val, &evals))
	require.NoError(t, err)

	inst, tr, ls := condInstance(t, def)
	inst.tracks[tr.ID()] = tr
	ls.active = 1

	ls.applyDehydrated(tr)

	require.Equal(t, 0, ls.active, "the released goroutine is no longer active")
	require.Contains(t, inst.tracks, tr.ID(),
		"the track record is retained (its wait is held externally)")
	require.Contains(t, ls.waiting, tr.ID(),
		"the wait registry entry is retained")
	require.Contains(t, ls.position, tr.ID(),
		"the token position is retained (projects Alive at the wait node)")
}

// TestTrackDehydratedStateString: the new state renders.
func TestTrackDehydratedStateString(t *testing.T) {
	require.Equal(t, "TrackDehydrated", TrackDehydrated.String())
	require.Equal(t, "dehydrated", evDehydrated.String())
}
