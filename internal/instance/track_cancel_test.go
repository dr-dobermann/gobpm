package instance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
)

// SRD-029 M2 — per-track cancellable context (FR-4). The loop derives a child
// context per track so it can interrupt one guarded track without touching its
// siblings, while instance terminate (the parent cancel) still cascades to all.

// twoParkedTracks builds one instance holding two independent signal-catch tracks,
// both parked in TrackWaitForEvent and neither spawned (the test drives run()).
func twoParkedTracks(t *testing.T) (*Instance, *track, *track) {
	t.Helper()

	inst, t1, _ := armTrack(t, "isolation", func(*mockeventproc.MockEventProducer) {})

	arm2, _, _ := ebSignalArm(t, "isolation-2")

	t2, err := newTrack(arm2, inst, nil)
	require.NoError(t, err)
	require.True(t, t2.inState(TrackWaitForEvent))

	return inst, t1, t2
}

// TestSpawnDerivesPerTrackCancel: the loop's spawn wires a per-track cancel
// (read after the loop has drained, so the write is observable race-free).
func TestSpawnDerivesPerTrackCancel(t *testing.T) {
	inst, _, stop := loopHarness(t)
	stop() // loop goroutine has finished spawning + draining.

	require.NotEmpty(t, inst.tracks)
	for _, tr := range inst.tracks {
		require.NotNil(t, tr.cancel, "spawn must derive a per-track cancel")
	}
}

// TestPerTrackCancelIsolation: cancelling one track's context ends only that
// track; the sibling runs on; the parent (instance) cancel then cascades to it.
func TestPerTrackCancelIsolation(t *testing.T) {
	inst, t1, t2 := twoParkedTracks(t)
	_ = inst

	// the instance context is the shared parent; each track gets a child of it,
	// exactly as the loop's spawn derives them (FR-4).
	parent, parentCancel := context.WithCancel(t.Context())
	defer parentCancel()

	c1, cancel1 := context.WithCancel(parent)
	t1.cancel = cancel1

	c2, cancel2 := context.WithCancel(parent)
	t2.cancel = cancel2
	defer cancel2()

	done1 := make(chan struct{})
	go func() { t1.run(c1); close(done1) }()

	done2 := make(chan struct{})
	go func() { t2.run(c2); close(done2) }()

	// interrupt ONLY t1.
	t1.cancel()

	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled track did not stop")
	}
	require.True(t, t1.inState(TrackCanceled))

	// the sibling must still be parked, untouched by t1's cancel.
	select {
	case <-done2:
		t.Fatal("a sibling track stopped on another track's cancel")
	case <-time.After(50 * time.Millisecond):
	}

	// instance terminate cancels the parent, cascading to the sibling (NFR-4).
	parentCancel()

	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("instance (parent) cancel did not cascade to the sibling")
	}
	require.True(t, t2.inState(TrackCanceled))
}
