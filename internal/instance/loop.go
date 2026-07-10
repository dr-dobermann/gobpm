package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	// aliased: the loop's human-task registry local is named `tasks`, which would
	// otherwise shadow the package name where the job map type is referenced.
	wtasks "github.com/dr-dobermann/gobpm/pkg/tasks"
)

// loop is the single owner of the Instance's lifecycle state (the tracks
// registry and the run state). Tracks never mutate that state directly — they
// emit events here, applied in order in this one goroutine, so no lock guards
// lifecycle state. The instance finishes when all tracks have ended.
func (inst *Instance) loop(ctx context.Context, initial []*track) {
	defer close(inst.loopDone)

	active := 0
	stopping := false
	// waiting holds tracks parked on their evtCh and not yet delivered (SRD-027 FR-4):
	// presence ⟺ parked-and-undelivered. Loop-goroutine-only, so no lock — like active /
	// stopping. evWaiting adds a track; the first evDeliver for it removes it and sends the
	// event (the winner); a later evDeliver for it finds it absent and drops (a losing arm of
	// an Event-Based gateway, or a duplicate fire).
	waiting := map[string]struct{}{}
	// msgIdx maps a waited Message catch-definition id → the parked track (SRD-027 FR-5/FR-8).
	// A track-less Message evDeliver (from Instance.ProcessEvent) is resolved through it; it is
	// seeded alongside waiting (by evWaiting / spawn) and a track is cleared from it the moment
	// it flips out of waiting or ends, so an index entry never outlives its track.
	msgIdx := map[string]*track{}
	// position is the loop-owned token-position view (SRD-028 FR-1): live trackID → its current
	// node. Seeded at spawn, advanced by evMoved, and cleared when a track dies (evEnded/evFailed/
	// evMerged-absorbed). Loop-goroutine-only — no lock, like waiting/msgIdx. The reachability and
	// join machinery read THIS map, never another track's currentStep cross-goroutine (Rule 2).
	position := map[string]flow.Node{}
	// parked is the loop-owned parked-at-join view (SRD-028 FR-3): trackID → join node, for tracks
	// suspended at a reachability/Complex join (TrackAwaitSync). Seeded on evParked from position,
	// cleared when the track resumes-and-moves, is merged away, or ends. recheckAwaitingJoins
	// iterates this map instead of scanning inst.tracks for the AwaitSync state.
	parked := map[string]flow.Node{}
	// watchers is the loop-owned boundary-subscription view (SRD-029 FR-5): trackID → the
	// boundaryWatch list armed while that track occupies a guarded activity. Armed when a track
	// arrives on an activity with interrupting boundaries (spawn / evMoved), torn down when it
	// leaves, ends, or fails. Loop-goroutine-only, like the maps above.
	watchers := map[string][]*boundaryWatch{}
	// tasks is the loop-owned human-task registry (SRD-034): taskID → the parked
	// UserTask track and node. Populated on evTaskWaiting (and at spawn for a task
	// parked at construction), read by a Take/Complete taskReq, and cleared when
	// the task completes or its track ends. Loop-goroutine-only, like the maps above.
	tasks := map[string]taskEntry{}
	// jobs is the loop-owned worker-job registry (SRD-036): JobID → the parked
	// worker-dispatched ServiceTask track. Populated on evJobWaiting (when the loop
	// enqueues the job), read by a worker's jobReq to resume the track, and cleared
	// when the job completes or its track ends. Loop-goroutine-only, like tasks.
	jobs := map[wtasks.JobID]*track{}

	// stopAll is forward-declared so spawn (which arms boundaries and faults the
	// instance on an arm failure) can reference it; it is assigned below.
	var stopAll func()

	// spawn registers a track, adds it to the read snapshot, counts it
	// active, and runs it in its own goroutine.
	spawn := func(t *track) {
		inst.tracks[t.ID()] = t
		inst.addToSnap(t)
		active++

		// Seed the track's initial position on the loop goroutine, BEFORE its run goroutine
		// starts (the `go` below). The track has no other goroutine yet, so this read is
		// sequential — not a Rule-2 cross-read; every later move arrives as evMoved (SRD-028 FR-2).
		position[t.ID()] = t.currentStep().node

		// A track built already parked (an event-start source waiter, registered
		// during New) is recorded here — on the loop goroutine, before its run
		// goroutine starts — so it is in `waiting` (and its message defs are in msgIdx)
		// before any evDeliver can target it (SRD-027 FR-5). Mid-run waits emit evWaiting
		// from checkNodeType instead.
		// A track that begins already parked (a start-event waiter, or a UserTask
		// reached as the initial node) is recorded here — on the loop goroutine,
		// before its run goroutine starts — so it is registered before any delivery
		// (SRD-027 FR-5, SRD-034).
		inst.recordBornWaiter(ctx, t, waiting, msgIdx, tasks)

		// Arm any interrupting boundary guarding the track's initial node — on the
		// loop goroutine, before its run goroutine starts — so the watcher exists
		// before the activity can complete or fire (SRD-029 FR-5). Subsequent moves
		// arm via evMoved. A non-activity initial node (a StartEvent) is a no-op.
		inst.armBoundaries(t, t.currentStep().node, watchers, stopAll)

		// Per-track cancellable context, derived here on the loop goroutine so
		// t.cancel is loop-owned — the loop is the sole caller that interrupts a
		// single track for an interrupting boundary (SRD-029 FR-4). inst.ctx stays
		// the parent, so instance terminate (inst.cancel) still cascades to every
		// track (NFR-4).
		tctx, cancel := context.WithCancel(ctx)
		t.cancel = cancel

		// run the track and report back to the loop. A track that reached a
		// synchronizing join without completing it ends its goroutine in
		// AwaitingMerge — reported as evAwaiting, not evEnded, so the loop keeps
		// it as awaiting (its run() will not resume).
		go func(t *track) {
			defer cancel()

			t.run(tctx)

			inst.emit(trackEvent{kind: trackEndKind(t), track: t})
		}(t)
	}

	// stopAll moves the instance to Terminating (once) and signals every
	// live track to stop.
	stopAll = func() {
		if stopping {
			return
		}

		stopping = true
		inst.setState(Terminating)

		for _, t := range inst.tracks {
			t.stop()
			// Cancel the track context so a running ctx-honoring activity (a ServiceTask
			// blocked in Exec) is interrupted — stopIt is only checked between nodes, not
			// mid-Exec (SRD-030 §3.3, the cooperative-cancellation contract of SRD-029).
			// On the abort/fault paths the instance ctx is already canceled, so this is a
			// no-op there (CancelFunc is idempotent).
			t.cancel()
			// Wake a track parked on evtCh: the loop is its sole sender, so closing
			// here is safe and doubles as teardown (SRD-027 FR-7). stopIt covers the
			// between-node path, t.cancel() the running one, the closed channel the parked one.
			close(t.evtCh)
		}

		inst.dropLoopState(waiting, msgIdx, position, parked, tasks, jobs)
	}

	for _, t := range initial {
		spawn(t)
	}

	if active == 0 {
		inst.setState(Completed)
		return
	}

	done := ctx.Done()
	for active > 0 {
		select {
		case <-done:
			done = nil
			stopAll()

		case ev := <-inst.events:
			// Lock-free attrs only (ID is immutable): this runs per event, and the
			// observability.Logger has no Enabled() gate, so the args are built even
			// at INFO. Node-level detail lives in the fire/abort logs below. A Message
			// evDeliver carries no track (FR-8), so the id is resolved defensively.
			inst.Logger().Debug("track event",
				"instance", inst.ID(),
				"kind", ev.kind.String(),
				"track", eventTrackID(ev))

			inst.applyEvent(ctx, ev, &active, &stopping,
				waiting, msgIdx, position, parked, watchers, tasks, jobs, spawn, stopAll)

		case req := <-inst.taskReq:
			// A human acting on a parked UserTask (Take/Complete). Serviced on the
			// loop goroutine so authorization/validation, scope access, and the
			// resume all stay single-writer (SRD-034 §4.1).
			inst.handleTaskRequest(ctx, req, tasks, waiting, msgIdx)

		case req := <-inst.jobReq:
			// A worker's terminal report (Complete/Fail) for a parked ServiceTask.
			// Serviced on the loop goroutine so the job→track resolution and the
			// resume stay single-writer, mirroring taskReq (SRD-036 §4.5).
			inst.handleJobCompletion(req, jobs, waiting, msgIdx)
		}
	}

	inst.settleFinalState(stopping)
}

// dropLoopState tears down the loop-owned maps when the instance stops (stopAll):
// after stop no track is dispatched to (a parked track is woken by its closed evtCh,
// not an evDeliver), so waiting/msgIdx are dropped to prevent a send on a closed
// channel (SRD-027 FR-7); the position/join view is no longer consulted (SRD-028
// FR-1/FR-3); parked UserTasks are withdrawn (SRD-034); and the worker-job registry
// is dropped — a terminating instance's jobs are no longer resumable, the enqueued
// jobs left for the dispatcher to expire (SRD-036).
func (inst *Instance) dropLoopState(
	waiting map[string]struct{},
	msgIdx map[string]*track,
	position, parked map[string]flow.Node,
	tasks map[string]taskEntry,
	jobs map[wtasks.JobID]*track,
) {
	clear(waiting)
	clear(msgIdx)
	clear(position)
	clear(parked)
	inst.withdrawAllTasks(tasks)
	clear(jobs)
}

// applyEvent applies one track→loop event to the loop-owned state on the loop goroutine.
// active and stopping are the loop's own counters, passed by pointer so this method and the
// spawn/stopAll closures mutate the same values; waiting is the loop-owned parked set and
// msgIdx its message-def→track index (SRD-027 FR-8); watchers is the loop-owned boundary
// subscription view (SRD-029 FR-5). Called only by loop().
func (inst *Instance) applyEvent(
	ctx context.Context,
	ev trackEvent,
	active *int,
	stopping *bool,
	waiting map[string]struct{},
	msgIdx map[string]*track,
	position, parked map[string]flow.Node,
	watchers map[string][]*boundaryWatch,
	tasks map[string]taskEntry,
	jobs map[wtasks.JobID]*track,
	spawn func(*track),
	stopAll func(),
) {
	switch ev.kind {
	case evFork:
		inst.spawnForks(ev, spawn, stopAll, *stopping)

	case evMoved:
		// the track advanced onto a new node — update the loop-owned position view and
		// clear any parked-at-join record (moving ⟹ not parked anymore). SRD-028 FR-2.
		// The track left its previous node, so tear down any boundaries that guarded it
		// there and arm those guarding the new node (SRD-029 FR-5/FR-6).
		inst.disarmBoundaries(ev.track.ID(), watchers)
		position[ev.track.ID()] = ev.node
		delete(parked, ev.track.ID())
		inst.armBoundaries(ev.track, ev.node, watchers, stopAll)

	case evEnded:
		*active--
		flipNotParked(ev.track, waiting, msgIdx)
		clearPosition(position, parked, ev.track)
		// a track that ended while owning a parked UserTask (canceled by an
		// interrupting boundary or instance terminate) has its task withdrawn and
		// dropped (SRD-034). A normal completion already removed it.
		inst.cleanupTask(ctx, ev.track, tasks)
		// a track that ended while owning a parked worker job drops its job entry
		// (the enqueued job is left for the dispatcher to expire — the engine has
		// no withdraw yet; a late report finds no track and is dropped). SRD-036.
		cleanupJob(ev.track, jobs)
		// the track's run() returned — its activity window (if any) is over, so
		// tear down the boundaries that guarded it (SRD-029 FR-6).
		inst.disarmBoundaries(ev.track.ID(), watchers)
		inst.recheckAwaitingJoins(position, parked, stopAll)

	case evAwaiting:
		// the track reached a synchronizing join, did not complete it, and its
		// goroutine returned — no longer active, but retained as awaiting until the
		// join fires (ADR-005 §2.4). Clear any index entry so it never outlives the track.
		// Its token is still Alive at the join, so it STAYS in position (not in parked —
		// AwaitingMerge is a Parallel join, not the AwaitSync reachability park). SRD-028 FR-6.
		*active--
		clearMsgIdx(msgIdx, ev.track)

	case evMerged:
		inst.applyMerged(ev, position, parked)
		inst.recheckAwaitingJoins(position, parked, stopAll)

	case evParked:
		inst.applyParked(ev, position, parked, stopAll, *stopping)

	case evFailed:
		inst.applyFailed(ev, active, waiting, msgIdx,
			position, parked, watchers, spawn, stopAll, *stopping)

	case evWaiting:
		inst.onWaiting(ev, *stopping, waiting, msgIdx)

	case evTaskWaiting:
		// a UserTask parked as a human task — register + announce it (SRD-034).
		inst.onTaskWaiting(ctx, ev, *stopping, waiting, tasks)

	case evJobWaiting:
		// a worker-dispatched ServiceTask parked — bind its input, enqueue the job,
		// and record it so the worker's report can resume it (SRD-036).
		inst.onJobWaiting(ctx, ev, *stopping, waiting, jobs)

	case evDeliver:
		inst.dispatchToParked(ctx, ev, waiting, msgIdx)

	case evBoundary:
		// an interrupting boundary fired over its guarded activity — cancel the host
		// track and continue on the boundary's exception flow, the loop arbitrating the
		// completion-vs-fire race (SRD-029 FR-5/FR-8).
		inst.fireBoundary(ev, watchers, spawn, stopAll, *stopping)

	case evTerminate:
		// a Terminate End Event was reached — abnormally terminate the instance (SRD-030
		// FR-1). stopAll sets stopping, tears down parked/between-node tracks, and cancels
		// each track's context to interrupt a running activity. It does NOT touch active:
		// the terminate track's own evEnded (FIFO-after this event) accounts for it.
		stopAll()
	}
}

// onWaiting records a track that parked on its evtCh as parked-and-undelivered and
// indexes its Message catch defs → track (SRD-027 FR-4/FR-5/FR-8). Skipped during
// shutdown: a parked track is then woken by its closed evtCh, not an evDeliver, and
// recording it would risk a send on the closed channel. Runs on the loop goroutine.
func (inst *Instance) onWaiting(
	ev trackEvent,
	stopping bool,
	waiting map[string]struct{},
	msgIdx map[string]*track,
) {
	if stopping {
		return
	}

	waiting[ev.track.ID()] = struct{}{}

	for _, id := range ev.msgDefIDs {
		msgIdx[id] = ev.track
	}
}

// dispatchToParked sends a fired event to its parked-and-undelivered track. The target is
// ev.track for a Signal/Timer evDeliver, or — for a track-less Message evDeliver (FR-8) —
// resolved from the fired definition's id via msgIdx (a miss is a benign drop). A message
// whose correlation does not match this conversation is gated here, on the loop goroutine —
// the sole owner of instance conversation state — and the track stays parked for the next
// message (SRD-027 §3.4 / NFR-2); Signal/Timer carry their track and are not correlated.
// On a match the flip (flipNotParked on first delivery) makes deferred choice atomic: a later
// event for the same track finds it absent and is dropped (a losing Event-Based-gateway arm
// or a duplicate fire). The loop is the sole sender to evtCh, and the single buffered slot
// keeps this send from blocking it (SRD-027 FR-3/FR-4). Called only by loop(), so it touches
// the loop-owned maps without a lock.
func (inst *Instance) dispatchToParked(
	ctx context.Context,
	ev trackEvent,
	waiting map[string]struct{},
	msgIdx map[string]*track,
) {
	tr := ev.track
	// Message (FR-8): a track-less evDeliver resolves the parked track from the fired def's id.
	if tr == nil {
		tr = msgIdx[ev.eDef.ID()]
		if tr == nil {
			return // no parked track for this message → drop
		}
	}

	if _, parked := waiting[tr.ID()]; !parked {
		return // losing arm / already delivered → drop (FR-4)
	}

	// Gate correlation only on the Message path (track == nil): a mismatch drops the event
	// and keeps the track parked for the next message (SRD-027 FR-8/NFR-2).
	if ev.track == nil && inst.validateAndAssociate(ctx, ev.eDef) {
		return // correlation mismatch — drop, keep the track parked
	}

	flipNotParked(tr, waiting, msgIdx)
	tr.evtCh <- ev.eDef
}

// flipNotParked removes tr from the parked set and clears its message-index entries — the
// atomic flip that makes deferred choice single-winner (SRD-027 FR-4/§3.4): a later event for
// tr finds it absent and is dropped. Also used on track end so no entry outlives its track.
func flipNotParked(
	tr *track,
	waiting map[string]struct{},
	msgIdx map[string]*track,
) {
	delete(waiting, tr.ID())
	clearMsgIdx(msgIdx, tr)
}

// clearMsgIdx removes every msgEDef→track entry pointing at tr, so a fired message can no
// longer resolve to a track that has flipped out of waiting or ended (SRD-027 §3.4).
func clearMsgIdx(msgIdx map[string]*track, tr *track) {
	for id, t := range msgIdx {
		if t == tr {
			delete(msgIdx, id)
		}
	}
}

// clearPosition drops tr from the loop-owned position and parked views — a dead track
// (Ended / Failed / Merged-absorbed) no longer holds a token, so it must not count as an
// occupied node or a parked-at-join arrival (SRD-028 FR-6).
func clearPosition(position, parked map[string]flow.Node, tr *track) {
	delete(position, tr.ID())
	delete(parked, tr.ID())
}

// eventTrackID returns the subject track's id for logging, or "<none>" for a track-less
// Message evDeliver (its target is resolved later via msgIdx — SRD-027 FR-8).
func eventTrackID(ev trackEvent) string {
	if ev.track == nil {
		return "<none>"
	}

	return ev.track.ID()
}

// nodeIDOf returns n.ID(), or "<none>" for a nil node — a defensive guard for log lines that
// read the loop-owned position map, where a miss yields a nil flow.Node (SRD-028 FR-5).
func nodeIDOf(n flow.Node) string {
	if n == nil {
		return "<none>"
	}

	return n.ID()
}

// trackEndKind classifies a track that returned from run() into the loop event
// kind: evAwaiting if it parked at a synchronizing join, evFailed if a node error
// left it TrackFailed (so the loop faults the instance — FIX-008), else the normal
// evEnded.
func trackEndKind(t *track) trackEventKind {
	switch {
	case t.inState(TrackAwaitingMerge):
		return evAwaiting

	case t.inState(TrackFailed):
		return evFailed

	default:
		return evEnded
	}
}

// failFromTrack surfaces a TrackFailed track's error as an instance failure: it
// records lastErr via Instance.fail (which also cancels the ctx so sibling tracks
// stop) and calls stopAll so the Terminating flag is set synchronously. When this
// is the last active track the ctx cancel alone would race the active--→loop-exit
// and the instance would settle on Completed instead of Terminated; stopAll makes
// the terminal state deterministic. Runs on the loop goroutine, the single writer
// of lastErr (FIX-008).
func (inst *Instance) failFromTrack(t *track, stopAll func()) {
	err := t.lastErr
	if err == nil {
		err = errs.New(
			errs.M("track %s failed", t.ID()),
			errs.C(errorClass, errs.OperationFailed))
	}

	inst.fail(err)
	stopAll()
}

// spawnForks builds and spawns one track per extra forked outgoing flow, runs
// each via spawn, and stops it immediately if the instance is already stopping.
// A build error is recorded and triggers stopAll. Called only from loop().
func (inst *Instance) spawnForks(
	ev trackEvent,
	spawn func(*track),
	stopAll func(),
	stopping bool,
) {
	for _, f := range ev.flows {
		nt, err := newTrack(f.Target().Node(), inst, ev.track)
		if err != nil {
			inst.lastErr.Store(&err)
			stopAll()

			return
		}

		// the new track reached its node via flow f; record it so a
		// synchronizing-join target knows the arriving incoming flow.
		nt.steps[0].inFlow = f

		inst.trackCount.Add(1)
		spawn(nt)

		if stopping {
			nt.stop()
		}
	}
}

// applyParked records a track that blocked at a reachability join (OR-join) in the
// loop-owned parked view and rechecks it (SRD-028 FR-3). Its goroutine is alive, so
// active is unchanged. Two guards:
//   - during shutdown stopAll has cleared the view and joins do not fire while
//     terminating; a late park is woken by ctx.Done(), not recorded here (mirrors
//     evWaiting);
//   - the track must still be live in the position view. If it is absent, the
//     completing arrival's evMerged was applied FIRST and already merged this
//     co-arriving track at the join (clearing its position) — its fate is settled, so
//     recording it now would re-insert a stale parked entry.
//
// Extracted from applyEvent to keep that switch under the complexity limit. Called
// only from applyEvent.
func (inst *Instance) applyParked(
	ev trackEvent,
	position, parked map[string]flow.Node,
	stopAll func(),
	stopping bool,
) {
	if stopping {
		return
	}

	if _, live := position[ev.track.ID()]; !live {
		return
	}

	parked[ev.track.ID()] = ev.node
	inst.recheckParked(ev.track, position, parked, stopAll)
}

// applyFailed handles a track failure on the loop goroutine (SRD-029 FR-9). It
// first tries to catch a typed BpmnError at an Error boundary on the failing
// activity (matchErrorBoundary, run before clearPosition so position still holds
// the failing node): a match routes to the boundary's exception flow and the
// instance runs on. Only an uncaught failure faults the instance (FIX-008). Then
// the track is cleared from the loop-owned views and its boundaries disarmed.
// Called only from applyEvent.
func (inst *Instance) applyFailed(
	ev trackEvent,
	active *int,
	waiting map[string]struct{},
	msgIdx map[string]*track,
	position, parked map[string]flow.Node,
	watchers map[string][]*boundaryWatch,
	spawn func(*track),
	stopAll func(),
	stopping bool,
) {
	if !inst.matchErrorBoundary(ev.track, position, spawn, stopAll, stopping) {
		inst.failFromTrack(ev.track, stopAll)
	}

	*active--
	flipNotParked(ev.track, waiting, msgIdx)
	clearPosition(position, parked, ev.track)
	inst.disarmBoundaries(ev.track.ID(), watchers)
}

// applyMerged flips the tracks the surviving track absorbed at a synchronizing
// join to Merged (their token projects Consumed). It resolves the absorbed ids
// against the loop-owned tracks map; the awaiting goroutines have already
// returned, so the loop is the sole writer of their state. Called only from
// loop().
func (inst *Instance) applyMerged(
	ev trackEvent, position, parked map[string]flow.Node,
) {
	survivor := ev.track.ID()

	for _, id := range ev.mergedIDs {
		m := inst.tracks[id]
		if m == nil {
			continue
		}

		m.mergedInto.Store(&survivor)
		m.updateState(TrackMerged)

		// the absorbed track is now dead (Merged) — drop it from the loop-owned position/
		// parked views so it stops counting as occupied or parked (SRD-028 FR-6).
		clearPosition(position, parked, m)

		// Wake the merged track unconditionally (FIX-006). If it is parked at a
		// reachability/Complex join (AwaitSync) it resumes and returns; if it has
		// not yet reached the park select, the buffered(1) signal waits for it; if
		// it already returned (Parallel AwaitingMerge) the signal is simply never
		// read. Gating this on "is it AwaitSync now" races the track's own
		// transition into AwaitSync and could miss it, hanging the instance.
		m.parkCh <- struct{}{}
	}
}

// recheckAwaitingJoins re-evaluates every reachability join currently holding a
// parked (AwaitSync) track — the death-trigger: a token death can make an
// un-marked incoming flow unreachable and fire a join that has no further arrival
// to ride (SRD-022 §2.10, fixing Camunda 7's arrival-only hang). Called only from
// loop() on a track end / merge.
func (inst *Instance) recheckAwaitingJoins(
	position, parked map[string]flow.Node, stopAll func(),
) {
	seen := map[string]bool{}

	// parked holds exactly the AwaitSync tracks (id → join node), so the loop reads its own
	// view instead of scanning inst.tracks for the state cross-goroutine (SRD-028 FR-3).
	for _, node := range parked {
		if seen[node.ID()] {
			continue
		}

		seen[node.ID()] = true
		inst.recheckJoin(node, position, parked, stopAll)
	}
}

// recheckParked handles a track that just parked at a reachability join. If the join
// already fired without recording this track (a late arrival deemed unreachable by an
// earlier fire — FIX-006), the track is a trailing token: consume it (flip to Merged
// and wake it so its goroutine returns). Otherwise recheck the join — a never-taken
// branch may already be unreachable, firing it now with no token death.
func (inst *Instance) recheckParked(
	t *track, position, parked map[string]flow.Node, stopAll func(),
) {
	// the join node is the one the track parked on, recorded in the loop-owned parked view
	// by the evParked case — no currentStep cross-read (SRD-028 FR-5).
	node := parked[t.ID()]

	if rj, ok := node.(exec.ReachabilityJoin); ok && rj.IsTrailing(t.ID()) {
		t.updateState(TrackMerged)
		// a trailing token is consumed (Merged) — drop it from the position/parked views so
		// it stops counting, matching today's "Merged ⇒ excluded" (SRD-028 FR-6).
		clearPosition(position, parked, t)
		t.parkCh <- struct{}{}

		return
	}

	inst.recheckJoin(node, position, parked, stopAll)
}

// recheckJoin re-evaluates a parked reachability join (OR-join) against the live
// token positions and fires it when no un-marked incoming flow can still receive
// a token (SRD-022 §2.10), or — for a complex (activation) join — aborts the instance
// when the rule is unsatisfiable (SRD-023). stopAll is the loop's terminate action,
// invoked on an abort so termination does not race the resulting track-end events
// (inst.fail alone only cancels ctx, leaving `stopping` unset). Called only from loop().
func (inst *Instance) recheckJoin(
	node flow.Node, position, parked map[string]flow.Node, stopAll func(),
) {
	// The loop-owned position/parked maps drive BOTH the in-transit guard and the reachability
	// the decision below uses (joinPositions), so they can't disagree — and neither reads a
	// track cross-goroutine (SRD-028 FR-4). An imminent arrival — a live token already on the
	// join node but not yet parked (between its evMoved onto the join and its evParked) — is
	// invisible to the backward reachability (it sits at the excluded join) and is not yet
	// marked. Defer: it re-triggers this recheck via its own evParked once it parks.
	occupied, inTransit := joinPositions(node, position, parked)
	if inTransit {
		return
	}

	fc := fixedFlowChecker{occupied: occupied}

	switch j := node.(type) {
	case exec.ActivationJoin:
		// Complex gateway (ADR-005 v.3 §2.11 / SRD-023): the loop owns the
		// fire/abort decision (with guard evaluation). A death can only make the
		// activation unsatisfiable — never newly fire it — so the abort path lives
		// here; firing resumes the parked survivor via fireOrJoin.
		dec, err := j.Recheck(inst.guardEval(inst.ctx), fc)

		switch {
		case err != nil:
			inst.fail(err)
			stopAll()

		case dec.Aborted:
			inst.fail(
				errs.New(
					errs.M("complex gateway activation rule is unsatisfiable"),
					errs.C(errorClass, errs.InvalidState),
					errs.D("node_id", node.ID())))
			stopAll()

		case dec.Fired:
			inst.fireOrJoin(dec.Survivor, dec.Merged, position, parked)
		}

	case exec.ReachabilityJoin:
		if complete, survivor, merged := j.Recheck(fc); complete {
			inst.fireOrJoin(survivor, merged, position, parked)
		}
	}
}

// fireOrJoin completes a reachability join: applyMerged flips the absorbed tracks to
// Merged and wakes any that are parked (FR-8 / FIX-006); here we only resume the
// survivor's blocked goroutine into the node. parkCh is buffered(1), so the signal
// never blocks the loop. Called only from loop().
func (inst *Instance) fireOrJoin(
	survivorID string, merged []string, position, parked map[string]flow.Node,
) {
	survivor := inst.tracks[survivorID]
	if survivor == nil {
		return
	}

	inst.Logger().Debug("synchronizing join fired",
		"instance", inst.ID(),
		"node", nodeIDOf(position[survivorID]),
		"survivor", survivorID,
		"merged", len(merged))

	inst.applyMerged(trackEvent{track: survivor, mergedIDs: merged}, position, parked)

	survivor.parkCh <- struct{}{}
}
