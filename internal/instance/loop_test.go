package instance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// newBareLoopInstance builds the minimal Iteration a direct loop-method call
// needs: the loop channels and an empty tracks registry, no engine wiring.
func newBareLoopInstance() *Instance {
	return &Instance{
		events:   make(chan trackEvent, 1),
		taskReq:  make(chan taskRequest),
		jobReq:   make(chan jobRequest),
		tracks:   map[string]*track{},
		loopDone: make(chan struct{}),

		// the engine-published registers a real iteration always has: code
		// under test writes them without checking, because New and Restore
		// both build them.
		performers:      newPerformers(),
		iterations:      newIterations(),
		iterationOwners: newIterationOwners(),
	}
}

// drainUntilEnd consumes loop events — standing in for the absent loop() in
// direct loopState tests — until the given track's terminal event arrives, so
// its run goroutine can exit (its emits would otherwise block forever on the
// unbuffered events channel).
func drainUntilEnd(t *testing.T, inst *Instance, trackID string) {
	t.Helper()

	for {
		select {
		case ev := <-inst.events:
			if ev.track != nil && ev.track.ID() == trackID {
				switch ev.kind {
				case evEnded, evFailed, evAwaiting:
					return
				}
			}

		case <-time.After(2 * time.Second):
			t.Fatalf("timed out draining events for track %s", trackID)
		}
	}
}

// newTrackIDs returns the track ids present in after but not in before — the
// tracks a loopState call spawned.
func newTrackIDs(before map[string]struct{}, inst *Instance) []string {
	var ids []string

	for id := range inst.tracks {
		if _, ok := before[id]; !ok {
			ids = append(ids, id)
		}
	}

	return ids
}

// trackIDSet snapshots the current track-id set for a later newTrackIDs diff.
func trackIDSet(inst *Instance) map[string]struct{} {
	ids := make(map[string]struct{}, len(inst.tracks))
	for id := range inst.tracks {
		ids[id] = struct{}{}
	}

	return ids
}

// TestLoopNoInitialTracksCompletes: a loop started with zero initial tracks
// settles the instance to Completed immediately and closes Done().
func TestLoopNoInitialTracksCompletes(t *testing.T) {
	inst := newBareLoopInstance()

	inst.loop(t.Context(), nil)

	require.Equal(t, Completed, inst.State())

	select {
	case <-inst.Done():
	default:
		t.Fatal("loop exit should close the Done channel")
	}
}

// TestOnWaitingStoppingDrops: once the loop is stopping, a late evWaiting is
// dropped — the track is not recorded as parked-and-undelivered (no delivery
// will ever target it; its evtCh is being closed by stopAll).
func TestOnWaitingStoppingDrops(t *testing.T) {
	ls := newLoopState(newBareLoopInstance())
	ls.stopping = true

	ls.onWaiting(t.Context(), trackEvent{})

	require.Empty(t, ls.waiting)
	require.Empty(t, ls.msgIdx)
}

// TestFireOrJoinUnknownSurvivor: a join fire whose survivor id is no longer in
// the tracks registry is a no-op, not a nil dereference — the survivor may
// have been torn down between the recheck and the fire.
func TestFireOrJoinUnknownSurvivor(t *testing.T) {
	ls := newLoopState(newBareLoopInstance())

	require.NotPanics(t, func() {
		ls.fireOrJoin("ghost", nil)
	})
}

// TestLoopStateDropClearsAll (SRD-040 T-4): drop() clears exactly the six maps
// dropLoopState cleared before the extraction — waiting, msgIdx, position,
// parked, jobs, and tasks (via withdrawAllTasks) — while watchers is
// deliberately untouched, as today: boundary teardown is per-track
// (disarmBoundaries), not part of the stop-path map drop.
func TestLoopStateDropClearsAll(t *testing.T) {
	inst := newBareLoopInstance()
	inst.td = interactor.NopDistributor()

	ls := newLoopState(inst)
	tr := &track{instance: inst}

	ls.waiting["t1"] = struct{}{}
	ls.msgIdx["m1"] = []msgSub{{track: tr}}
	ls.position["t1"] = nil
	ls.parked["t1"] = nil
	ls.watchers["t1"] = []*boundaryWatch{{}}
	ls.tasks["task1"] = taskEntry{track: tr}
	ls.jobs["job1"] = tr

	ls.drop()

	require.Empty(t, ls.waiting)
	require.Empty(t, ls.msgIdx)
	require.Empty(t, ls.position)
	require.Empty(t, ls.parked)
	require.Empty(t, ls.tasks)
	require.Empty(t, ls.jobs)
	require.Len(t, ls.watchers, 1, "watchers are not cleared by drop()")
}

// TestLoopStateOnTaskWaitingStoppingDrops: once the loop is stopping, a late
// evTaskWaiting is dropped — the task is neither recorded nor announced (it is
// torn down by stopAll, not completed).
func TestLoopStateOnTaskWaitingStoppingDrops(t *testing.T) {
	ls := newLoopState(newBareLoopInstance())
	ls.stopping = true

	ls.onTaskWaiting(t.Context(), trackEvent{})

	require.Empty(t, ls.waiting)
	require.Empty(t, ls.tasks)
}

// TestLoopStateOnJobWaitingStoppingDrops: once the loop is stopping, a late
// evJobWaiting is dropped — no job is enqueued or registered (the enqueued
// work would never be resumable).
func TestLoopStateOnJobWaitingStoppingDrops(t *testing.T) {
	ls := newLoopState(newBareLoopInstance())
	ls.stopping = true

	ls.onJobWaiting(t.Context(), trackEvent{})

	require.Empty(t, ls.waiting)
	require.Empty(t, ls.jobs)
}

// TestLoopStateCleanupTaskDropsOwned: cleanupTask withdraws and drops exactly
// the tasks owned by the ended track, leaving other tracks' tasks parked.
func TestLoopStateCleanupTaskDropsOwned(t *testing.T) {
	inst := newBareLoopInstance()
	inst.td = interactor.NopDistributor()

	ls := newLoopState(inst)
	ended := &track{instance: inst}
	other := &track{instance: inst}

	ls.tasks["owned"] = taskEntry{track: ended}
	ls.tasks["parked"] = taskEntry{track: other}

	ls.cleanupTask(t.Context(), ended)

	require.NotContains(t, ls.tasks, "owned", "the ended track's task is dropped")
	require.Contains(t, ls.tasks, "parked", "another track's task stays parked")
}

// TestLoopStateApplyMergedGhost: a merged id that is no longer in the tracks
// registry is skipped, not dereferenced — the absorbed track may already be
// gone by the time the merge event is applied.
func TestLoopStateApplyMergedGhost(t *testing.T) {
	inst := newBareLoopInstance()
	ls := newLoopState(inst)

	be, err := foundation.NewBaseElement()
	require.NoError(t, err)
	survivor := &track{BaseElement: *be, instance: inst}

	require.NotPanics(t, func() {
		ls.applyMerged(trackEvent{track: survivor, mergedIDs: []string{"ghost"}})
	})
}

// TestApplyPendingCancel is FIX-038 T-10 at the level the mechanism lives: a
// cancel aimed at a DEHYDRATED instance has no loop to receive it, so it rides
// the rebuild and the fresh loop must apply it BEFORE its park decision.
//
// stopAll is what makes it stick — it sets ls.stopping, which maybeDehydrate
// checks, so the instance cannot park again before observing the request. A
// bare context cancel would race the re-park and be lost exactly as the
// pre-rebuild cancel was.
func TestApplyPendingCancel(t *testing.T) {
	inst := newBareLoopInstance()
	inst.td = interactor.NopDistributor()

	resp := make(chan error, 1)

	// the option is the carrier: it is what a rebuild is handed.
	cfg := newConfig{}
	WithPendingCancel(resp)(&cfg)
	require.NotNil(t, cfg.pendingCancel, "the option arms the rebuild")

	inst.pendingCancel = cfg.pendingCancel

	ls := newLoopState(inst)
	ls.applyPendingOps(t.Context())

	require.True(t, ls.stopping,
		"the cancel tears the instance down, so it cannot park again")
	require.Equal(t, Terminating, inst.State())
	require.Nil(t, inst.pendingCancel, "a pending cancel is consumed once")

	select {
	case err := <-resp:
		require.NoError(t, err, "the caller is answered")

	default:
		t.Fatal("the cancel verdict never reached the caller")
	}
}

// TestApplyPendingOpsWithNothingPending: a rebuild carrying neither operator
// request leaves the loop alone — the ordinary wake must not be torn down.
func TestApplyPendingOpsWithNothingPending(t *testing.T) {
	ls := newLoopState(newBareLoopInstance())

	ls.applyPendingOps(t.Context())

	require.False(t, ls.stopping, "an ordinary rebuild is not a cancel")
}

// TestLoopStateApplySequences (SRD-040 T-5): a loopState driven directly with
// evMoved/evParked sequences maintains the position and parked views per the
// SRD-028 semantics — the unit-level access the loopState extraction unlocks.
func TestLoopStateApplySequences(t *testing.T) {
	inst := newBareLoopInstance()
	ls := newLoopState(inst)
	ls.active = 1

	be, err := foundation.NewBaseElement()
	require.NoError(t, err)
	tr := &track{BaseElement: *be, instance: inst}

	// plain end events stand in for process nodes (no boundaries → the
	// arm/disarm calls inside evMoved are no-ops).
	nodeA, err := events.NewEndEvent("a")
	require.NoError(t, err)
	nodeB, err := events.NewEndEvent("b")
	require.NoError(t, err)

	// evMoved advances the position view.
	ls.apply(t.Context(), trackEvent{kind: evMoved, track: tr, node: nodeA})
	require.Equal(t, nodeA, ls.position[tr.ID()])

	// evMoved onto another node re-points the view and clears a stale park.
	ls.parked[tr.ID()] = nodeA
	ls.apply(t.Context(), trackEvent{kind: evMoved, track: tr, node: nodeB})
	require.Equal(t, nodeB, ls.position[tr.ID()])
	require.NotContains(t, ls.parked, tr.ID(),
		"moving clears the parked-at-join record")

	// evAwaiting decrements active and keeps the position (the token is still
	// alive at the join — SRD-028 FR-6).
	ls.apply(t.Context(), trackEvent{kind: evAwaiting, track: tr})
	require.Zero(t, ls.active)
	require.Contains(t, ls.position, tr.ID(),
		"an awaiting track keeps its position")
}
