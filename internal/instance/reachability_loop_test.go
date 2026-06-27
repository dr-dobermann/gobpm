package instance

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// TestJoinPositions covers the pure derivation of occupied/in-transit from the loop-owned
// position/parked maps (SRD-028 §3.4): no instance, no tracks — only the maps the loop owns.
func TestJoinPositions(t *testing.T) {
	_, split, a, _, merge := orDiamond(t)

	t.Run("token on the join, not parked → in-transit",
		func(t *testing.T) {
			occupied, inTransit := joinPositions(merge,
				map[string]flow.Node{"t1": merge},
				map[string]flow.Node{})
			require.True(t, inTransit)
			require.Equal(t, map[string]bool{merge.ID(): true}, occupied)
		})

	t.Run("token on the join, parked there → settled, not in-transit",
		func(t *testing.T) {
			_, inTransit := joinPositions(merge,
				map[string]flow.Node{"t1": merge},
				map[string]flow.Node{"t1": merge})
			require.False(t, inTransit)
		})

	t.Run("token elsewhere → occupied, not in-transit",
		func(t *testing.T) {
			occupied, inTransit := joinPositions(merge,
				map[string]flow.Node{"t1": split, "t2": a},
				map[string]flow.Node{})
			require.False(t, inTransit)
			require.Equal(t,
				map[string]bool{split.ID(): true, a.ID(): true}, occupied)
		})

	t.Run("empty position → empty occupied, not in-transit",
		func(t *testing.T) {
			occupied, inTransit := joinPositions(merge,
				map[string]flow.Node{}, map[string]flow.Node{})
			require.False(t, inTransit)
			require.Empty(t, occupied)
		})

	t.Run("nil join node → occupied only, never in-transit",
		func(t *testing.T) {
			occupied, inTransit := joinPositions(nil,
				map[string]flow.Node{"t1": merge}, map[string]flow.Node{})
			require.False(t, inTransit)
			require.Equal(t, map[string]bool{merge.ID(): true}, occupied)
		})
}

// TestApplyEventMoved covers the evMoved case (SRD-028 FR-2): the loop advances its own
// position view and clears any stale parked-at-join record for the moving track.
func TestApplyEventMoved(t *testing.T) {
	p, split, a, _, _ := orDiamond(t)
	inst := newDiamondInstance(t, p)

	tr, err := newTrack(split, inst, nil)
	require.NoError(t, err)

	position := map[string]flow.Node{tr.ID(): split}
	parked := map[string]flow.Node{tr.ID(): split} // a stale park to be cleared on move

	active := 1
	stopping := false

	inst.applyEvent(context.Background(),
		trackEvent{kind: evMoved, track: tr, node: a},
		&active, &stopping,
		map[string]struct{}{}, map[string]*track{}, position, parked,
		func(*track) {}, func() {})

	require.Equal(t, a, position[tr.ID()], "position advanced to the new node")
	_, stillParked := parked[tr.ID()]
	require.False(t, stillParked, "moving clears the parked-at-join record")
}

// TestRecheckJoinNonReachability covers the defensive guard: recheckJoin on a node that is not
// a ReachabilityJoin (the orDiamond uses Parallel gateways) is a no-op over the loop-owned maps.
func TestRecheckJoinNonReachability(t *testing.T) {
	p, split, _, _, _ := orDiamond(t)
	inst := newDiamondInstance(t, p)

	require.NotPanics(t, func() {
		inst.recheckJoin(split,
			map[string]flow.Node{}, map[string]flow.Node{}, func() {})
	})
}

// TestRecheckAwaitingJoinsIteratesParked covers the death-recheck reading the loop-owned parked
// view (SRD-028 FR-3) instead of scanning tracks: with no parked tracks it does nothing, and with
// a parked (non-reachability) node it dedupes by node and rechecks without panicking.
func TestRecheckAwaitingJoinsIteratesParked(t *testing.T) {
	p, split, _, _, _ := orDiamond(t)
	inst := newDiamondInstance(t, p)

	t.Run("empty parked → no-op",
		func(t *testing.T) {
			require.NotPanics(t, func() {
				inst.recheckAwaitingJoins(
					map[string]flow.Node{}, map[string]flow.Node{}, func() {})
			})
		})

	t.Run("two tracks parked at one node → one recheck, deduped",
		func(t *testing.T) {
			parked := map[string]flow.Node{"t1": split, "t2": split}
			require.NotPanics(t, func() {
				inst.recheckAwaitingJoins(
					map[string]flow.Node{"t1": split, "t2": split}, parked, func() {})
			})
		})
}

// TestRecheckParkedTrailing covers the trailing-token branch (FIX-006, SRD-028 FR-5/FR-6): a
// token that parks at an OR-join which has ALREADY fired is consumed (Merged), woken, and dropped
// from the loop-owned position/parked views.
func TestRecheckParkedTrailing(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("or-trailing")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	split, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)
	join, err := gateways.NewInclusiveGateway(
		gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, join, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, split)
	link(t, split, join)
	link(t, split, join)
	link(t, join, end)

	inst := newDiamondInstance(t, p)

	// Fire the OR-join with one arrival, the other incoming flow unreachable (empty occupied).
	inFlow := join.Incoming()[0]
	complete, _ := join.Arrive(inFlow.ID(), "winner")
	require.False(t, complete, "1 of 2 incoming → not complete on arrival")
	fired, _, _ := join.Recheck(fixedFlowChecker{occupied: map[string]bool{}})
	require.True(t, fired, "the join fires when the other branch is unreachable")

	// A token now parks at the already-fired join — a trailing arrival.
	tr, err := newTrack(join, inst, nil)
	require.NoError(t, err)
	require.True(t, join.IsTrailing(tr.ID()))

	position := map[string]flow.Node{tr.ID(): join}
	parked := map[string]flow.Node{tr.ID(): join}

	inst.recheckParked(tr, position, parked, func() {})

	require.True(t, tr.inState(TrackMerged), "a trailing token is consumed (Merged)")
	require.NotContains(t, position, tr.ID(), "dropped from the position view")
	require.NotContains(t, parked, tr.ID(), "dropped from the parked view")

	select {
	case <-tr.parkCh:
	default:
		require.Fail(t, "the trailing track must be woken on parkCh")
	}
}

// TestApplyEventParkedDuringShutdown covers the shutdown guard of the evParked case (SRD-028
// FR-3): once the loop is stopping, stopAll has cleared the parked view and joins do not fire,
// so a late park must NOT be recorded — otherwise it would leave a stale entry a later
// recheckAwaitingJoins would walk.
func TestApplyEventParkedDuringShutdown(t *testing.T) {
	p, split, _, _, _ := orDiamond(t)
	inst := newDiamondInstance(t, p)

	tr, err := newTrack(split, inst, nil)
	require.NoError(t, err)

	position := map[string]flow.Node{} // cleared, as stopAll leaves it
	parked := map[string]flow.Node{}
	active := 1
	stopping := true

	require.NotPanics(t, func() {
		inst.applyEvent(context.Background(),
			trackEvent{kind: evParked, track: tr, node: split},
			&active, &stopping,
			map[string]struct{}{}, map[string]*track{}, position, parked,
			func(*track) {}, func() {})
	})

	require.Empty(t, parked, "a park during shutdown is not recorded")
}

// TestApplyEventParkedAfterMerge covers the merge-race guard of the evParked case (SRD-028
// FR-3): when all branches arrive at an OR-join, the completing arrival's evMerged can be
// applied before a co-arriving track's own evParked, clearing it from position. The late park
// must then be dropped (the track is already merged) rather than re-inserted as a stale entry.
func TestApplyEventParkedAfterMerge(t *testing.T) {
	p, split, _, _, _ := orDiamond(t)
	inst := newDiamondInstance(t, p)

	tr, err := newTrack(split, inst, nil)
	require.NoError(t, err)

	position := map[string]flow.Node{} // tr already cleared by an earlier evMerged
	parked := map[string]flow.Node{}
	active := 0
	stopping := false

	require.NotPanics(t, func() {
		inst.applyEvent(context.Background(),
			trackEvent{kind: evParked, track: tr, node: split},
			&active, &stopping,
			map[string]struct{}{}, map[string]*track{}, position, parked,
			func(*track) {}, func() {})
	})

	require.Empty(t, parked, "a park for an already-merged track is dropped")
}

// TestNodeIDOf covers the nil-node guard of the position log helper (SRD-028 FR-5).
func TestNodeIDOf(t *testing.T) {
	require.Equal(t, "<none>", nodeIDOf(nil))

	_, split, _, _, _ := orDiamond(t)
	require.Equal(t, split.ID(), nodeIDOf(split))
}
