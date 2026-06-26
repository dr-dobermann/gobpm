package instance

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestJoinPositionsInTransit covers the in-transit guard of the single-snapshot reader: a live
// track sitting on a node (pre-park) is an imminent arrival; a parked or absent one is not.
func TestJoinPositionsInTransit(t *testing.T) {
	p, split, _, _, merge := orDiamond(t)
	inst := newDiamondInstance(t, p)

	tr, err := newTrack(split, inst, nil)
	require.NoError(t, err)
	inst.tracks[tr.ID()] = tr

	_, inTransit := inst.joinPositions(split)
	require.True(t, inTransit,
		"a live (Ready) track on the node is an imminent arrival")

	_, inTransit = inst.joinPositions(merge)
	require.False(t, inTransit, "no track on the node")

	tr.updateState(TrackAwaitSync)

	_, inTransit = inst.joinPositions(split)
	require.False(t, inTransit, "a parked (AwaitSync) track is not in transit")
}

// TestRecheckJoinNonReachability covers the defensive guard: recheckJoin on a node
// that is not a ReachabilityJoin (the orDiamond uses Parallel gateways) is a no-op.
func TestRecheckJoinNonReachability(t *testing.T) {
	p, split, _, _, _ := orDiamond(t)
	inst := newDiamondInstance(t, p)

	require.NotPanics(t, func() { inst.recheckJoin(split, func() {}) })
}

// TestRecheckAwaitingJoinsNoneAwaiting covers the empty pass: with no parked
// tracks, the death-recheck does nothing.
func TestRecheckAwaitingJoinsNoneAwaiting(t *testing.T) {
	p, split, _, _, _ := orDiamond(t)
	inst := newDiamondInstance(t, p)

	tr, err := newTrack(split, inst, nil)
	require.NoError(t, err)
	inst.tracks[tr.ID()] = tr // Ready, not AwaitSync — skipped

	require.NotPanics(t, func() { inst.recheckAwaitingJoins(func() {}) })
}
