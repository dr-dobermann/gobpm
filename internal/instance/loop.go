package instance

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/observability"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/dr-dobermann/gobpm/pkg/set"

	// aliased: the loop's human-task registry field is named `tasks`, which would
	// otherwise shadow the package name where the job map type is referenced.
	wtasks "github.com/dr-dobermann/gobpm/pkg/tasks"
)

// loopState is the single-writer loop's registry state (ADR-001, ADR-017,
// SRD-040). It is created by loop() and lives only on the loop goroutine: no
// locks — goroutine confinement is the synchronization. It must never be
// stored on Instance or escape the loop; every method below runs on the loop
// goroutine only.
type loopState struct {
	inst *Instance

	// waiting holds tracks parked on their evtCh and not yet delivered (SRD-027 FR-4):
	// presence ⟺ parked-and-undelivered. evWaiting adds a track; the first evDeliver for
	// it removes it and sends the event (the winner); a later evDeliver for it finds it
	// absent and drops (a losing arm of an Event-Based gateway, or a duplicate fire).
	waiting map[string]struct{}
	// msgIdx maps a waited Message catch-definition id → the ORDERED
	// subscription list of parked tracks (SRD-027 FR-5/FR-8, SRD-085
	// FR-2): one keyless entry routes directly (the single-waiter
	// case); several entries route by the iteration-correlation match,
	// and a second entry is refused outright when either is keyless —
	// ambiguous delivery is a loud modeling error (ADR-006 v.5 §2.9.3).
	// A track-less Message evDeliver (from Instance.ProcessEvent) is resolved through it; it is
	// seeded alongside waiting (by evWaiting / spawn) and a track is cleared from it the moment
	// it flips out of waiting or ends, so an index entry never outlives its track.
	msgIdx map[string][]msgSub
	// position is the loop-owned token-position view (SRD-028 FR-1): live trackID → its current
	// node. Seeded at spawn, advanced by evMoved, and cleared when a track dies (evEnded/evFailed/
	// evMerged-absorbed). The reachability and join machinery read THIS map, never another
	// track's currentStep cross-goroutine (Rule 2).
	position map[string]flow.Node
	// parked is the loop-owned parked-at-join view (SRD-028 FR-3): trackID → join node, for tracks
	// suspended at a reachability/Complex join (TrackAwaitSync). Seeded on evParked from position,
	// cleared when the track resumes-and-moves, is merged away, or ends. recheckAwaitingJoins
	// iterates this map instead of scanning inst.tracks for the AwaitSync state.
	parked map[string]flow.Node
	// watchers is the loop-owned boundary-subscription view (SRD-029 FR-5): trackID → the
	// boundaryWatch list armed while that track occupies a guarded activity. Armed when a track
	// arrives on an activity with interrupting boundaries (spawn / evMoved), torn down when it
	// leaves, ends, or fails.
	watchers map[string][]*boundaryWatch
	// retryC delivers the earliest scheduled incident-retry deadline (SRD-079
	// §3.4) — ONE Clock.After per instance, re-armed on every incident
	// mutation; nil (blocking its select case) when nothing is scheduled.
	retryC <-chan time.Time
	// tasks is the loop-owned human-task registry (SRD-034): taskID → the parked
	// UserTask track and node. Populated on evTaskWaiting (and at spawn for a task
	// parked at construction), read by a Take/Complete taskReq, and cleared when
	// the task completes or its track ends.
	tasks map[string]taskEntry
	// jobs is the loop-owned worker-job registry (SRD-036): JobID → the parked
	// worker-dispatched ServiceTask track. Populated on evJobWaiting (when the loop
	// enqueues the job), read by a worker's jobReq to resume the track, and cleared
	// when the job completes or its track ends.
	jobs map[wtasks.JobID]*track
	// calls is the loop-owned in-flight Call Activity registry (SRD-050 FR-6):
	// child instance id → the parked caller track, its CallActivity node, and
	// the child handle. Populated on evCallWaiting (when the loop launches the
	// child), read by the watcher's callReq to resume the track, and cleared
	// when the call completes or its caller track ends.
	calls map[string]*callEntry
	// scopeHandlers is the loop-owned Event Sub-Process registry (SRD-052 FR-5):
	// a scope path → the handlers armed while that scope is open (the
	// boundary-watch pattern at scope granularity). Armed on scope open
	// (armScopeHandlers), torn down on scope drain/cancel (disarmScopeHandlers).
	scopeHandlers map[scope.DataPath][]*scopeHandlerWatch
	// scopeInterrupted is the loop-owned shared interrupting budget (SRD-052
	// FR-6): a scope path → whether an interrupting handler has already fired in
	// it. The FIRST interrupting fire — an event sub-process (fireScopeHandler)
	// OR a boundary event on the scope host (fireBoundary via cancelHostScope) —
	// flips the scope, and every later interrupting fire in the same scope is
	// suppressed. Keyed by the composite's INNER scope path, the one both
	// constructs reference, so the two cooperate rather than double-fire (NFR-1).
	scopeInterrupted map[scope.DataPath]bool
	// scopes is the loop-owned nested-scope registry (SRD-049 FR-9): open
	// child path → its entry (parked host, composite node, drain counter,
	// re-entry queue). Opened by an activity instance's executor,
	// drained by the terminal-event accounting, closed + host-resumed at
	// zero.
	scopes map[scope.DataPath]*scopeEntry
	// ledgers is the per-scope compensation completion ledger (ADR-026 §2.1,
	// SRD-059 FR-3): completion-ordered compensable entries with their data
	// snapshots, keyed by the scope path they completed in (the root path
	// included — it has no scopeEntry but its completions ledger the same way).
	// Loop-owned; folded child→parent at completeScope, discarded when the
	// enclosing scope finishes or cancels.
	ledgers map[scope.DataPath][]*ledgerEntry
	// sweeps ties each in-flight compensation-handler track to its sweep
	// (SRD-059 FR-6): the sweep advances on the handler's evEnded and aborts
	// on its evFailed. Loop-owned.
	sweeps map[string]*sweepRun
	// iter mirrors each own-iteration host's decorator position for the
	// checkpoint capture (SRD-082 FR-2), keyed by host track id.
	// Loop-owned; dropped when the host track ends.
	iter map[string]*iterMirror
	// conds is the loop-owned armed-conditional registry (SRD-048 FR-8): a SLICE,
	// because arming order is the multi-fire contract (fires from one commit apply
	// in arming order — ADR-006 v.3 §2.7). Armed by evWaiting / recordBornWaiter,
	// swept on evDataCommit, torn down when the owning track flips out of waiting,
	// ends, or fails (flipNotParked → clearConds).
	conds []*condWatch

	// handlerSeq mints a unique child-scope segment for each non-interrupting
	// Event Sub-Process fire (SRD-053), so concurrent handler instances of the
	// same node open distinct scopes instead of queueing behind one path.
	// Monotonic, loop-owned.
	handlerSeq int
	// active counts the tracks whose run goroutine has not yet reported a
	// terminal event; the loop exits when it returns to zero.
	active int
	// stopping is set once by stopAll: the instance is Terminating, late parks
	// and fires are dropped, and the loop only drains the remaining track ends.
	stopping bool
	// dehydrating is set by maybeDehydrate once the instance is fully idle and
	// every parked track has been released (SRD-071 FR-2): the loop tail then
	// parks the instance (Dehydrated) instead of settling it Completed/Terminated.
	dehydrating bool
}

// newLoopState builds the loop's empty registry state over its instance.
func newLoopState(inst *Instance) *loopState {
	return &loopState{
		inst:             inst,
		waiting:          map[string]struct{}{},
		msgIdx:           map[string][]msgSub{},
		position:         map[string]flow.Node{},
		parked:           map[string]flow.Node{},
		watchers:         map[string][]*boundaryWatch{},
		tasks:            map[string]taskEntry{},
		jobs:             map[wtasks.JobID]*track{},
		calls:            map[string]*callEntry{},
		scopeHandlers:    map[scope.DataPath][]*scopeHandlerWatch{},
		scopeInterrupted: map[scope.DataPath]bool{},
		scopes:           map[scope.DataPath]*scopeEntry{},
		iter:             map[string]*iterMirror{},
		ledgers:          map[scope.DataPath][]*ledgerEntry{},
		sweeps:           map[string]*sweepRun{},
	}
}

// adoptRestored applies the checkpoint-rebuilt state the loop adopts at
// start: the compensation ledger (SRD-070 FR-6), the derived composite
// scope entries (SRD-082 FR-5 — BEFORE the spawns count tracks into
// them) and the re-linked in-flight calls (FR-7). False means the
// adoption failed loud and the loop must not start.
func (inst *Instance) adoptRestored(
	ctx context.Context, ls *loopState, initial []*track,
) bool {
	if inst.restoredLedgers != nil {
		ls.ledgers = inst.restoredLedgers
	}

	err := ls.adoptRestoredScopes(initial)
	if err == nil {
		err = ls.adoptRestoredAdHoc(initial)
	}

	if err == nil {
		err = ls.adoptRestoredCalls(ctx)
	}

	if err != nil {
		// fail() is phase-only; the loop is exiting without running, so
		// the lifecycle must land terminal too — a stuck Active instance
		// would look healthy forever (SRD-082 NFR-2).
		inst.fail(err)
		inst.setState(Terminated)

		return false
	}

	return true
}

// spawnInitial spawns the initial tracks and then resumes the restored
// compensation sweeps (SRD-082 FR-6) — after the spawns, so the
// handler tracks count into their scopes' drain accounting like live
// ones. False means the sweep adoption failed loud and terminal.
func (inst *Instance) spawnInitial(
	ctx context.Context, ls *loopState, initial []*track,
) bool {
	for _, t := range initial {
		ls.spawn(ctx, t)
	}

	if err := ls.adoptRestoredSweeps(ctx); err != nil {
		inst.fail(err)
		inst.setState(Terminated)

		return false
	}

	// a stopped ad-hoc container with nothing live completes here — the
	// spawns above have counted every restored track into its scope, so
	// the drain check is over the real accounting (SRD-083 FR-4).
	ls.completeRestoredAdHoc(ctx)

	return true
}

// loop is the single owner of the Instance's lifecycle state (the tracks
// registry and the run state). Tracks never mutate that state directly — they
// emit events here, applied in order in this one goroutine, so no lock guards
// lifecycle state. The instance finishes when all tracks have ended.
func (inst *Instance) loop(ctx context.Context, initial []*track) {
	defer close(inst.loopDone)

	ls := newLoopState(inst)

	// A restored instance adopts its checkpoint-rebuilt state; a fresh
	// one adopts nothing and proceeds.
	if !inst.adoptRestored(ctx, ls, initial) {
		return
	}

	if !inst.spawnInitial(ctx, ls, initial) {
		return
	}

	// arm the process's top-level Event Sub-Process handlers at the instance
	// root scope — they guard the whole instance's window (SRD-052 FR-5).
	ls.armScopeHandlers(ctx, rootNodes(inst), inst.sc.root)

	ls.applyPendingOps(ctx)

	if !ls.loopPreflight() {
		return
	}

	// The activation checkpoint (SRD-070 FR-4): the instance's first
	// durable record — created-and-running with its seeded data.
	ls.checkpointNow(ctx)

	// An instance can be fully idle the moment it starts: every initial track
	// BORN parked on a held, dehydratable wait (an event-born instance whose
	// fired start leads straight to a catch; a UserTask as the entry node). No
	// event will ever arrive to trigger the post-event check below — the loop
	// would simply block forever holding goroutines it does not need — so the
	// detector runs once here too (SRD-071 FR-2).
	ls.maybeDehydrate(ctx)

	done := ctx.Done()
	for ls.active > 0 || ls.pendingRetries() > 0 {
		done = ls.drainCancel(done)

		select {
		case <-done:
			done = nil
			ls.stopAll()

		case <-ls.retryC:
			// a scheduled incident retry came due (SRD-079 §3.4): respawn,
			// then persist the new attempt under the incident transition.
			ls.applyDueIncidentRetries(ctx)
			ls.maybeCheckpoint(ctx, evIncident)

		case ev := <-inst.events:
			// Lock-free attrs only (ID is immutable): this runs per event, and the
			// observability.Logger has no Enabled() gate, so the args are built even
			// at INFO. Node-level detail lives in the fire/abort logs below. A Message
			// evDeliver carries no track (FR-8), so the id is resolved defensively.
			inst.Logger().Debug("track event",
				observability.AttrInstanceID, inst.ID(),
				"kind", ev.kind.String(),
				observability.AttrTrackID, eventTrackID(ev))

			ls.apply(ctx, ev)
			ls.maybeCheckpoint(ctx, ev.kind)
			ls.maybeDehydrate(ctx)

		case req := <-inst.taskReq:
			// A human acting on a parked UserTask (Take/Complete). Serviced on the
			// loop goroutine so authorization/validation, scope access, and the
			// resume all stay single-writer (SRD-034 §4.1).
			ls.handleTaskRequest(ctx, req)

		case req := <-inst.jobReq:
			// A worker's terminal report (Complete/Fail) for a parked ServiceTask.
			// Serviced on the loop goroutine so the job→track resolution and the
			// resume stay single-writer, mirroring taskReq (SRD-036 §4.5).
			ls.handleJobCompletion(req)

		case req := <-inst.callReq:
			// A watcher's report that a Call Activity's child instance ended.
			// Serviced on the loop goroutine so the output binding and the resume
			// stay single-writer, mirroring jobReq (SRD-050 FR-7).
			ls.handleCallCompletion(req)

		case req := <-inst.incidentReq:
			// An operator incident operation (SRD-079 §3.6): retry-now,
			// resolve, drop. Serviced on the loop goroutine so the incident
			// table, the spawns and the checkpoint stay single-writer.
			ls.handleIncidentRequest(ctx, req)

		case req := <-inst.scopeReq:
			// A looped composite's off-loop iteration decorator asking to open the
			// child scope for a pass; serviced on the loop goroutine so OpenScope /
			// seed / arm stay single-writer, mirroring taskReq (SRD-054 FR-8a).
			ls.handleScopeRequest(ctx, req)
		}
	}

	ls.exitLoop(ctx)
}

// exitLoop settles the loop's end: a dehydration or an incident park returns
// without settling — the instance is not finishing, it is waiting — and only
// a genuinely finished instance tears down, settles its final state and
// writes the terminal checkpoint.
func (ls *loopState) exitLoop(ctx context.Context) {
	inst := ls.inst

	// Dehydration exit (SRD-071 FR-2): the loop released every goroutine while
	// idle — the instance is not finishing, it is parking. Set Dehydrated —
	// which emits the KindInstanceState/Dehydrated fact at Info (FR-10, the
	// wake counterpart Hydrated rides the engine's wake) — and take the
	// consistent-cut checkpoint (its tracks are now TrackDehydrated, the
	// hydration source); do NOT disarm handlers, discard ledgers, or settle a
	// terminal state — a trigger hydrates it back.
	if ls.dehydrating {
		inst.setStateDetailed(Dehydrated, ls.dehydrationDetails())
		ls.checkpointNow(ctx)

		return
	}

	if ls.parkOnIncidents(ctx) {
		return
	}

	// the main flow is done — disarm any still-armed Event Sub-Process handlers
	// (a normal completion never runs drop, so root-scope handlers are torn
	// down here; idempotent if a terminate already dropped them). SRD-052 FR-5.
	ls.disarmAllScopeHandlers()

	// the enclosing scope of every remaining ledger — the instance itself — is
	// finishing: the eligibility window closes, remaining entries discard
	// (ADR-006 §2.3, SRD-059 FR-3/NFR-3).
	ls.discardLedgers(inst.sc.root)

	inst.settleFinalState(ls.stopping)

	// The instance is genuinely FINISHED — release anyone waiting on
	// completion. The dehydration exit above deliberately skips this: a
	// released instance has not finished, it has merely stopped occupying
	// goroutines (SRD-071).
	inst.markSettled()

	// The terminal checkpoint (SRD-070 FR-4): the record flips to its
	// terminal status. WithoutCancel: a Terminate arrives BY canceling
	// ctx, and the terminal record is the one write that must not be
	// lost to that cancellation.
	ls.checkpointNow(context.WithoutCancel(ctx))
}

// spawn registers a track, adds it to the read snapshot, counts it active, and
// runs it in its own goroutine. Called only on the loop goroutine.
func (ls *loopState) spawn(ctx context.Context, t *track) {
	ls.inst.tracks[t.ID()] = t
	ls.inst.addToSnap(t)
	ls.active++
	ls.incScope(t)

	// Seed the track's initial position on the loop goroutine, BEFORE its run goroutine
	// starts (the `go` below). The track has no other goroutine yet, so this read is
	// sequential — not a Rule-2 cross-read; every later move arrives as evMoved (SRD-028 FR-2).
	ls.position[t.ID()] = t.currentStep().node

	// A track that begins already parked (a start-event waiter, or a UserTask
	// reached as the initial node) is recorded here — on the loop goroutine,
	// before its run goroutine starts — so it is in `waiting` (and its message
	// defs are in msgIdx) before any evDeliver can target it (SRD-027 FR-5,
	// SRD-034). Mid-run waits emit evWaiting from checkNodeType instead.
	ls.recordBornWaiter(ctx, t)

	// Per-track cancellable context, derived here on the loop goroutine so
	// t.cancel is loop-owned — the loop is the sole caller that interrupts a
	// single track for an interrupting boundary (SRD-029 FR-4). inst.ctx stays
	// the parent, so instance terminate (inst.cancel) still cascades to every
	// track (NFR-4). Set BEFORE armBoundaries: an arm-time-true interrupting
	// Conditional boundary fires during arming and cancels the host at once
	// (SRD-048 FR-9/FR-15).
	tctx, cancel := context.WithCancel(ctx)
	t.cancel = cancel

	// Arm any interrupting boundary guarding the track's initial node — on the
	// loop goroutine, before its run goroutine starts — so the watcher exists
	// before the activity can complete or fire (SRD-029 FR-5). Subsequent moves
	// arm via evMoved. A non-activity initial node (a StartEvent) is a no-op.
	ls.armBoundaries(ctx, t, t.currentStep().node)

	// run the track and report back to the loop. A track that reached a
	// synchronizing join without completing it ends its goroutine in
	// AwaitingMerge — reported as evAwaiting, not evEnded, so the loop keeps
	// it as awaiting (its run() will not resume).
	go func(t *track) {
		defer cancel()

		t.run(tctx)

		ls.inst.emit(trackEvent{kind: trackEndKind(t), track: t})
	}(t)
}

// applyPendingOps runs the operator requests that rode a rebuild, BEFORE the
// park decision — so neither is lost to an immediate re-park (SRD-079 §3.6,
// FIX-038 §1.10). A parked instance has no loop to receive them, which is why
// they travel with the rebuild instead of being delivered to one.
func (ls *loopState) applyPendingOps(ctx context.Context) {
	inst := ls.inst

	// An incident op: a retry/resolve spawns tracks that keep the loop alive,
	// a drop parks again — either way the op is applied first.
	if req := inst.pendingIncidentOp; req != nil {
		inst.pendingIncidentOp = nil
		ls.handleIncidentRequest(ctx, *req)
	}

	// A cancel. stopAll — not inst.Cancel — is what makes it stick: it sets
	// ls.stopping, which maybeDehydrate checks, so the instance cannot park
	// again before observing the request. Canceling the context alone would
	// race the re-park and be lost exactly as the pre-rebuild cancel was.
	if req := inst.pendingCancel; req != nil {
		inst.pendingCancel = nil

		ls.stopAll()

		if req.resp != nil {
			req.resp <- nil
		}
	}
}

// stopAll moves the instance to Terminating (once) and signals every live
// track to stop. Called only on the loop goroutine.
func (ls *loopState) stopAll() {
	if ls.stopping {
		return
	}

	ls.stopping = true
	ls.inst.setState(Terminating)

	for _, t := range ls.inst.tracks {
		// withdraw any engine-level holds this wait registered (SRD-071 FR-3):
		// the instance is finishing, so neither its deadlines nor its
		// subscriptions may outlive it.
		t.releaseHolds()

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

	ls.drop()
}

// drop tears down the loop-owned maps when the instance stops (stopAll):
// after stop no track is dispatched to (a parked track is woken by its closed evtCh,
// not an evDeliver), so waiting/msgIdx are dropped to prevent a send on a closed
// channel (SRD-027 FR-7); the position/join view is no longer consulted (SRD-028
// FR-1/FR-3); parked UserTasks are withdrawn (SRD-034); and the worker-job registry
// is dropped — a terminating instance's jobs are no longer resumable, the enqueued
// jobs left for the dispatcher to expire (SRD-036).
func (ls *loopState) drop() {
	clear(ls.waiting)
	clear(ls.msgIdx)
	ls.conds = nil
	clear(ls.scopes)
	clear(ls.scopeInterrupted)
	clear(ls.position)
	clear(ls.parked)
	ls.withdrawAllTasks()
	clear(ls.jobs)
	// terminate every in-flight Call Activity's child before dropping the
	// registry: a child instance runs under the engine's context (not the
	// parent's), so a terminating parent does NOT auto-cancel it — the cascade
	// is explicit (SRD-050 FR-9).
	for _, entry := range ls.calls {
		entry.child.Terminate()
	}
	clear(ls.calls)
	// unregister every armed Event Sub-Process handler's hub waiter — the
	// instance is terminating (SRD-052 FR-5).
	ls.disarmAllScopeHandlers()
}

// apply applies one track→loop event to the loop-owned state on the loop
// goroutine. Called only by loop().
func (ls *loopState) apply(ctx context.Context, ev trackEvent) {
	switch ev.kind {
	case evFork:
		ls.spawnForks(ctx, ev)

	case evMoved:
		// the track advanced onto a new node — update the loop-owned position view and
		// clear any parked-at-join record (moving ⟹ not parked anymore). SRD-028 FR-2.
		// The track left its previous node, so tear down any boundaries that guarded it
		// there and arm those guarding the new node (SRD-029 FR-5/FR-6).
		// Moving off a node is its successful completion — if it is a leaf with
		// a Compensation boundary, it enters the completion ledger (SRD-059
		// FR-3; a failed node arrives as evFailed, so only successes ledger).
		if departed := ls.position[ev.track.ID()]; departed != nil {
			ls.recordLeafCompletion(ev, departed)
		}
		ls.disarmBoundaries(ev.track.ID())
		ls.position[ev.track.ID()] = ev.node
		delete(ls.parked, ev.track.ID())
		ls.armBoundaries(ctx, ev.track, ev.node)
		// a retry attempt moving past its incident's node closed the
		// incident — the retry succeeded (SRD-079 §3.4).
		ls.closeIncidentsOnProgress(ev.track)

	case evEnded:
		// a compensation-handler track's end advances its sweep (SRD-059
		// FR-6) — checked first, before the standard accounting.
		ls.compensationTrackEnded(ctx, ev.track, false)
		ls.active--
		delete(ls.iter, ev.track.ID())
		ls.decScope(ctx, ev.track)
		ls.flipNotParked(ev.track)
		ls.clearPosition(ev.track)
		// a track that ended WITHOUT its wait firing — canceled by an
		// interrupting boundary, most often — still owns whatever that wait
		// held, and a deadline or subscription outliving the wait it belongs to
		// would wake a later cycle for a track that no longer exists (SRD-071
		// FR-3b). A normal completion already released on delivery, and
		// releaseHolds is idempotent, so this is a no-op there.
		ev.track.releaseHolds()
		// a track that ended while owning a parked UserTask (canceled by an
		// interrupting boundary or instance terminate) has its task withdrawn and
		// dropped (SRD-034). A normal completion already removed it.
		ls.cleanupTask(ctx, ev.track)
		// a track that ended while owning a parked worker job drops its job entry
		// (the enqueued job is left for the dispatcher to expire — the engine has
		// no withdraw yet; a late report finds no track and is dropped). SRD-036.
		ls.cleanupJob(ev.track)
		// a track that ended while owning an in-flight Call Activity terminates
		// its child (the cascade — the child ends with the caller) and drops the
		// entry; a late watcher report then finds no entry and is dropped (SRD-050).
		ls.cleanupCall(ev.track)
		// the track's run() returned — its activity window (if any) is over, so
		// tear down the boundaries that guarded it (SRD-029 FR-6).
		ls.disarmBoundaries(ev.track.ID())
		ls.recheckAwaitingJoins()

	case evAwaiting:
		// the track reached a synchronizing join, did not complete it, and its
		// goroutine returned — no longer active, but retained as awaiting until the
		// join fires (ADR-005 §2.4). Clear any index entry so it never outlives the track.
		// Its token is still Alive at the join, so it STAYS in position (not in parked —
		// AwaitingMerge is a Parallel join, not the AwaitSync reachability park). SRD-028 FR-6.
		ls.active--
		ls.decScope(ctx, ev.track)
		ls.clearMsgIdx(ev.track)

	case evMerged:
		ls.applyMerged(ev)
		ls.recheckAwaitingJoins()

	case evParked:
		ls.applyParked(ev)

	case evFailed:
		// a failing compensation handler aborts its sweep (the thrower
		// resumes — its wait is over) and then faults through the ordinary
		// Error chain below: `Compensating → Failed` (SRD-059 FR-6/T-9).
		ls.compensationTrackEnded(ctx, ev.track, true)
		ls.applyFailed(ctx, ev)

	case evWaiting, evTaskWaiting, evJobWaiting, evCallWaiting, evDeliver,
		evDataCommit, evDehydrated:
		// the wait/deliver plane — parks, deliveries, and the signals that
		// re-evaluate or resume them; sub-dispatched to keep apply under the
		// complexity limit (the applyParked precedent).
		ls.applyWaitPlane(ctx, ev)

	case evBoundary:
		// an interrupting boundary fired over its guarded activity — cancel the host
		// track and continue on the boundary's exception flow, the loop arbitrating the
		// completion-vs-fire race (SRD-029 FR-5/FR-8).
		ls.fireBoundary(ctx, ev)

	case evScopeHandlerFire:
		// a scope-armed Event Sub-Process handler's trigger fired — the
		// scope-level peer of evBoundary (ADR-023 v.2 §2.10, SRD-052 FR-7).
		ls.fireScopeHandler(ctx, ev)

	case evEscalate, evCompensate:
		// a non-fault throw propagation: an escalation walks the scope chain
		// (SRD-058 FR-1/FR-2), a compensation resolves against the completion
		// ledger (SRD-059 FR-5/FR-6); neither tears down the throwing track.
		ls.applyThrowPropagation(ctx, ev)

	case evScopeTerminate, evTransactionCancel:
		// an end event inside a sub-process resolved loop-locally — sub-dispatched
		// to keep apply under the complexity limit (the applyThrowPropagation
		// precedent).
		ls.applyScopeAbort(ctx, ev)

	case evTerminate:
		// a Terminate End Event was reached — abnormally terminate the instance (SRD-030
		// FR-1). stopAll sets stopping, tears down parked/between-node tracks, and cancels
		// each track's context to interrupt a running activity. It does NOT touch active:
		// the terminate track's own evEnded (FIFO-after this event) accounts for it.
		ls.stopAll()
	}
}

// applyScopeAbort dispatches the scope-local end-event resolutions: a scoped
// Terminate kills only its own scope while the parent continues (§13.5.6,
// SRD-049 FR-11); a Transaction Cancel aborts its enclosing Transaction —
// compensate the completed activities, terminate the residuals, exit via the
// Cancel boundary (ADR-028 §2.3, SRD-061 FR-5). Called only by apply, on the
// loop goroutine.
func (ls *loopState) applyScopeAbort(ctx context.Context, ev trackEvent) {
	switch ev.kind {
	case evScopeTerminate:
		ls.terminateScope(ctx, ev.track.scopePath)

	case evTransactionCancel:
		ls.cancelTransaction(ctx, ev.track)
	}
}

// applyWaitPlane dispatches the wait/deliver-plane events (parks,
// deliveries, the commit signal, the scope open) — the apply sub-switch.
// Called only by apply, on the loop goroutine.
func (ls *loopState) applyWaitPlane(ctx context.Context, ev trackEvent) {
	switch ev.kind {
	case evDehydrated:
		// a track's long-wait goroutine was released (SRD-071 FR-1):
		// account it out, retain its wait record.
		ls.applyDehydrated(ev.track)

	case evWaiting:
		ls.onWaiting(ctx, ev)

	case evTaskWaiting:
		// a UserTask parked as a human task — register + announce it (SRD-034).
		ls.onTaskWaiting(ctx, ev)

	case evJobWaiting:
		// a worker-dispatched ServiceTask parked — bind its input, enqueue the job,
		// and record it so the worker's report can resume it (SRD-036).
		ls.onJobWaiting(ctx, ev)

	case evCallWaiting:
		// a Call Activity parked — resolve its inputs at the caller's scope,
		// launch the child instance, and record it so the watcher's completion
		// report can resume the track (SRD-050).
		ls.onCallWaiting(ctx, ev)

	case evDeliver:
		ls.dispatchToParked(ctx, ev)

	case evDataCommit:
		// a node's frame commit changed data — sweep the armed conditionals:
		// re-evaluate the due ones, fire on false→true edges in arming order
		// (SRD-048 FR-11).
		ls.sweepConditionals(ctx, ev.changes)
	}
}

// onWaiting records a track that parked on its evtCh as parked-and-undelivered and
// indexes its Message catch defs → track (SRD-027 FR-4/FR-5/FR-8). Skipped during
// shutdown: a parked track is then woken by its closed evtCh, not an evDeliver, and
// recording it would risk a send on the closed channel. Runs on the loop goroutine.
func (ls *loopState) onWaiting(ctx context.Context, ev trackEvent) {
	if ls.stopping {
		return
	}

	ls.waiting[ev.track.ID()] = struct{}{}

	for _, id := range ev.msgDefIDs {
		ls.addMsgSub(id, ev.track)
	}

	// arm the track's conditional subscriptions AFTER it is recorded parked,
	// so an arm-time fire can deliver through the normal parked-dispatch
	// contract (SRD-048 FR-9). The subscribed node rides the emit — the
	// loop-owned position may still hold the previous node here (evWaiting
	// precedes evMoved).
	ls.armConditionalsAt(ctx, ev.track, ev.node)
}

// msgSub is one waiting subscription for a message definition (SRD-085
// FR-2): the parked track and its iteration-correlation pair — the
// declared key's name and this execution's value ("" = keyless).
type msgSub struct {
	track   *track
	keyName string
	value   string
}

// addMsgSub records a parked track's message subscription (SRD-085
// FR-2/FR-4). Ambiguity refuses loud: a SECOND subscription for one
// definition when either entry is keyless leaves delivery with no rule
// to pick by — a modeling error the instance faults on rather than an
// arbitrary pick (ADR-006 v.5 §2.9.3). Runs on the loop goroutine.
func (ls *loopState) addMsgSub(id string, t *track) {
	subs := ls.msgIdx[id]

	for _, sub := range subs {
		if sub.track == t {
			return // re-parked — already indexed
		}
	}

	t.m.RLock()
	keyName, value := t.msgIterKeyName, t.msgIterKey
	t.m.RUnlock()

	// keyless means NO DECLARATION (keyName empty) — never an
	// evaluated value that happens to be "": an iteration whose split
	// item is the empty string is correlated like any other, and
	// reading it as keyless would fault a well-formed model.
	if len(subs) > 0 && (keyName == "" || subs[0].keyName == "") {
		ls.inst.fail(errs.New(
			errs.M("message definition %q has %d concurrent waiters but "+
				"no iteration correlation on every one of them — "+
				"delivery would be ambiguous (declare "+
				"WithIterationCorrelation on the catch)",
				id, len(subs)+1),
			errs.C(errorClass, errs.InvalidState)))
		ls.stopAll()

		return
	}

	ls.msgIdx[id] = append(subs, msgSub{
		track: t, keyName: keyName, value: value,
	})
}

// resolveMsgSub picks the ONE subscription an arriving message serves
// (SRD-085 FR-2, ADR-006 v.5 §2.9.2): a single keyless subscription
// routes directly (the pre-SRD-085 contract); keyed subscriptions
// route by deriving the declared key's envelope-side value and
// matching it — no match leaves every waiter parked for the next
// message. Runs on the loop goroutine.
func (ls *loopState) resolveMsgSub(
	ctx context.Context, subs []msgSub, eDef flow.EventDefinition,
) *track {
	if len(subs) == 0 {
		return nil
	}

	if len(subs) == 1 && subs[0].keyName == "" {
		return subs[0].track
	}

	for _, sub := range subs {
		derived, ok := ls.inst.corr.deriveNamed(ctx, eDef, sub.keyName)
		if ok && derived == sub.value {
			return sub.track
		}
	}

	return nil
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
func (ls *loopState) dispatchToParked(ctx context.Context, ev trackEvent) {
	tr := ev.track
	// Message (FR-8): a track-less evDeliver resolves the parked track from the
	// fired def's id — by the iteration-correlation match when several wait
	// (SRD-085 FR-2).
	if tr == nil {
		tr = ls.resolveMsgSub(ctx, ls.msgIdx[ev.eDef.ID()], ev.eDef)
		if tr == nil {
			return // no (matching) parked track for this message → drop
		}
	}

	if _, parked := ls.waiting[tr.ID()]; !parked {
		return // losing arm / already delivered → drop (FR-4)
	}

	// Gate correlation only on the Message path (track == nil): a mismatch drops the event
	// and keeps the track parked for the next message (SRD-027 FR-8/NFR-2).
	if ev.track == nil && ls.inst.corr.validateAndAssociate(ctx, ev.eDef) {
		return // correlation mismatch — drop, keep the track parked
	}

	// AN ITERATED ACTIVITY'S DELIVERY GOES TO ITS INSTANCES, not to the
	// track they share (SRD-090.B FR-3). The decorator says which are
	// waiting; the rule for how many receive it is the trigger's own.
	if ev.iterProc != nil {
		ls.dispatchToInstances(tr, ev)

		return
	}

	ls.flipNotParked(tr)
	tr.evtCh <- ev.eDef
}

// dispatchToInstances routes one occurrence to the instances of an iterated
// activity waiting for it (SRD-090.B FR-3, ADR-006 §2.9.2).
//
// The multiplicity is the KIND's, stated as contract rather than left to
// mechanism: a Signal reaches every waiting instance (publication fan-out),
// a Timer and a Conditional likewise since each instance's trigger is its
// own, and a Message reaches exactly ONE — it is point-to-point, so serving
// N would let N instances consume one envelope and all complete.
//
// Ordinal order is normative where the instances are otherwise
// indistinguishable: nothing else can decide which receives an envelope, and
// two runs of one model must not disagree about it.
func (ls *loopState) dispatchToInstances(tr *track, ev trackEvent) {
	ords := ev.iterProc.waitingOn(ev.eDef.ID())
	if len(ords) == 0 {
		return // nobody waits on it any more — drop, as a losing arm does
	}

	// EXACTLY ONE INSTANCE, the first still waiting in ordinal order.
	//
	// A Message is point-to-point (ADR-006 §2.9.2), and a Message is the
	// only trigger an iterated leaf can carry: `ReceiveTask` is the sole
	// iterable activity that is a `flow.EventNode`, and its one definition
	// is a message. A UserTask and an external-worker ServiceTask park
	// through capabilities and never reach this path, and an event catch
	// cannot iterate at all — loop characteristics belong to activities.
	//
	// So the broadcast half of the rule — a Signal or Timer serving EVERY
	// waiting instance — has no expressible case today. It stays decided in
	// ADR-006 §2.9.2 rather than written here as code nothing can exercise;
	// the slice that makes such a wait expressible implements it against a
	// test that can fail.
	//
	// Ordinal order is what makes "the first" reproducible: with nothing to
	// tell the instances apart, two runs of one model must not disagree
	// about which received an envelope. Telling them APART is the
	// correlated case, and it needs the key per instance rather than per
	// track (SRD-090.B M5c).
	ev.iterProc.deliverTo(ords[0], ev.eDef)

	// the TRACK leaves the parked set only once no instance of its activity
	// waits: it is one entry standing for N waiters, so flipping it on the
	// first delivery would drop every later occurrence at the gate above
	// (the same reason the activity's hold outlives all but the last).
	if !ev.iterProc.anyWaiting() {
		ls.flipNotParked(tr)
	}
}

// flipNotParked removes tr from the parked set and clears its message-index and
// armed-conditional entries — the atomic flip that makes deferred choice
// single-winner (SRD-027 FR-4/§3.4, SRD-048 FR-14): a later event for tr finds
// it absent and is dropped. Also used on track end so no entry outlives its track.
func (ls *loopState) flipNotParked(tr *track) {
	delete(ls.waiting, tr.ID())
	ls.clearMsgIdx(tr)
	ls.clearConds(tr.ID())
}

// clearMsgIdx removes every subscription of tr, so a fired message can no
// longer resolve to a track that has flipped out of waiting or ended
// (SRD-027 §3.4); tr's iteration key leaves the hub-facing filter with it
// (SRD-085 FR-3).
func (ls *loopState) clearMsgIdx(tr *track) {
	for id, subs := range ls.msgIdx {
		kept := subs[:0]

		for _, sub := range subs {
			if sub.track != tr {
				kept = append(kept, sub)
			}
		}

		if len(kept) == 0 {
			delete(ls.msgIdx, id)

			continue
		}

		ls.msgIdx[id] = kept
	}

	ls.inst.corr.dropIterKey(tr.ID())
}

// clearPosition drops tr from the loop-owned position and parked views — a dead track
// (Ended / Failed / Merged-absorbed) no longer holds a token, so it must not count as an
// occupied node or a parked-at-join arrival (SRD-028 FR-6).
func (ls *loopState) clearPosition(tr *track) {
	delete(ls.position, tr.ID())
	delete(ls.parked, tr.ID())
}

// noneLabel marks an absent subject (a track-less event, a nil node) in log
// and fact attributes.
const noneLabel = "<none>"

// eventTrackID returns the subject track's id for logging, or noneLabel for a track-less
// Message evDeliver (its target is resolved later via msgIdx — SRD-027 FR-8).
func eventTrackID(ev trackEvent) string {
	if ev.track == nil {
		return noneLabel
	}

	return ev.track.ID()
}

// nodeIDOf returns n.ID(), or noneLabel for a nil node — a defensive guard for log lines that
// read the loop-owned position map, where a miss yields a nil flow.Node (SRD-028 FR-5).
func nodeIDOf(n flow.Node) string {
	if n == nil {
		return noneLabel
	}

	return n.ID()
}

// trackEndKind classifies a track that returned from run() into the loop event
// kind: evAwaiting if it parked at a synchronizing join, evFailed if a node error
// left it TrackFailed (so the loop faults the instance — FIX-008), else the normal
// evEnded.
func trackEndKind(t *track) trackEventKind {
	switch {
	case t.inState(TrackDehydrated):
		return evDehydrated

	case t.inState(TrackAwaitingMerge):
		return evAwaiting

	case t.inState(TrackFailed):
		return evFailed

	default:
		return evEnded
	}
}

// drainCancel records a cancellation the loop can ALREADY see, before any
// further terminal-event accounting. `select` chooses uniformly among ready
// cases (Go spec), so a canceled — and therefore permanently ready — done can
// lose every turn while evEnded drives ls.active to zero; the loop would then
// exit with ls.stopping false and settle Completed for an instance that was
// torn down (ADR-001 v.6 §7, FIX-033 §2.1). Returns nil once canceled, so the
// check costs nothing afterwards.
func (ls *loopState) drainCancel(done <-chan struct{}) <-chan struct{} {
	if done == nil {
		return nil
	}

	select {
	case <-done:
		ls.stopAll()

		return nil

	default:
		return done
	}
}

// loopPreflight decides the loop's entry after the initial spawns. A restored
// incident instance may start with no live track at all — its continuations
// are the incident records: scheduled retries drive it into the loop, an
// operator-waiting one parks again without settling, and only a truly empty
// instance completes. It also arms the incident-retry timer (a restored
// deadline may already be due). Reports whether the loop should run.
func (ls *loopState) loopPreflight() bool {
	ls.rearmIncidentRetryTimer()

	if ls.active > 0 {
		return true
	}

	if ls.inst.openIncidents() == 0 && ls.inst.deadLetters() == 0 {
		ls.inst.setState(Completed)

		return false
	}

	return ls.pendingRetries() > 0
}

// parkOnIncidents is the loop's incident exit (ADR-036 §2.2, SRD-079 §3.2):
// when every remaining continuation is an open incident, the instance is not
// finishing — it is waiting for a retry or an operator. Like the dehydration
// exit: no settle, no handler teardown, no ledger discard; the incident
// records carry the continuations. A stopping instance settles normally — a
// terminate overrides waiting. Reports whether the loop parked.
func (ls *loopState) parkOnIncidents(ctx context.Context) bool {
	if ls.stopping ||
		(ls.inst.openIncidents() == 0 && ls.inst.deadLetters() == 0) {
		return false
	}

	ls.checkpointNow(ctx)

	return true
}

// failFromTrack surfaces a TrackFailed track's error as an instance failure: it
// records lastErr via Instance.fail (which also cancels the ctx so sibling tracks
// stop) and calls stopAll so the Terminating flag is set synchronously. When this
// is the last active track the ctx cancel alone would race the active--→loop-exit
// and the instance would settle on Completed instead of Terminated; stopAll makes
// the terminal state deterministic. Runs on the loop goroutine, the single writer
// of lastErr (FIX-008).
func (ls *loopState) failFromTrack(t *track) {
	err := t.lastErr
	if err == nil {
		err = errs.New(
			errs.M("track %s failed", t.ID()),
			errs.C(errorClass, errs.OperationFailed))
	}

	ls.inst.fail(err)
	ls.stopAll()
}

// spawnForks builds and spawns one track per extra forked outgoing flow, runs
// each via spawn, and stops it immediately if the instance is already stopping.
// A build error is recorded and triggers stopAll. Called only from the loop
// goroutine.
func (ls *loopState) spawnForks(ctx context.Context, ev trackEvent) {
	for _, s := range ev.succs {
		nt, err := newTrack(s.node, ls.inst, ev.track)
		if err != nil {
			// A fork target that can't be built is a genuine instance fault —
			// route it through fail() (the single logging fault boundary,
			// ADR-022 v.1 §2.3) instead of storing lastErr silently, mirroring
			// failFromTrack / armBoundaries. fail() also cancels the ctx.
			ls.inst.fail(err)
			ls.stopAll()

			return
		}

		// the new track reached its node via a sequence flow; record it so a
		// synchronizing-join target knows the arriving incoming flow. A
		// Router-chosen successor carries none and leaves it nil (SRD-074 §3.1).
		nt.steps[0].inFlow = s.inFlow

		ls.inst.trackCount.Add(1)
		ls.spawn(ctx, nt)

		if ls.stopping {
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
// Extracted from apply to keep that switch under the complexity limit. Called
// only from apply.
func (ls *loopState) applyParked(ev trackEvent) {
	if ls.stopping {
		return
	}

	if _, live := ls.position[ev.track.ID()]; !live {
		return
	}

	ls.parked[ev.track.ID()] = ev.node
	ls.recheckParked(ev.track)
}

// applyFailed handles a track failure on the loop goroutine (SRD-029 FR-9). It
// first tries to catch a typed BpmnError at an Error boundary on the failing
// activity (matchErrorBoundary, run before clearPosition so position still holds
// the failing node): a match routes to the boundary's exception flow and the
// instance runs on. Only an uncaught failure faults the instance (FIX-008). Then
// the track is cleared from the loop-owned views and its boundaries disarmed.
// Called only from apply.
func (ls *loopState) applyFailed(ctx context.Context, ev trackEvent) {
	caught := ls.matchErrorBoundary(ctx, ev.track) ||
		ls.matchErrorScopeChain(ctx, ev.track)

	incident := false

	if !caught {
		ls.reportUncaught(ev.track)

		// The failure taxonomy (ADR-036 §2.1, SRD-079 §3.2): an invariant
		// violation denies the engine's own state; an Error End Event's
		// uncaught throw is the model's own verdict; and a CHILD instance
		// propagates every uncaught failure across the call boundary — to
		// its caller the whole called process is a single task, and the
		// incident arises only at the top-level call node. All three keep
		// the fatal path; every other uncaught failure — technical or an
		// activity's uncaught BpmnError — becomes a durable incident and
		// the instance runs on.
		if isInvariantFailure(ev.track.lastErr) ||
			isModeledErrorEnd(ev.track) ||
			ls.inst.parentInstanceID != "" {
			ls.failFromTrack(ev.track)
		} else {
			ls.raiseIncident(ctx, ev.track)
			incident = true

			// the raise is a persist point of its own (FR-5): evFailed is
			// not in checkpointTransitions, so the incident checkpoint is
			// requested explicitly under its own kind.
			ls.maybeCheckpoint(ctx, evIncident)
		}
	}

	ls.active--

	// An open incident pins its scope (ADR-036 §2.2): retry re-enters against
	// that data, so the scope must not drain while the incident holds it. The
	// deferred release is part of the incident's close (the resolution slice).
	if !incident {
		ls.decScope(ctx, ev.track)
	}

	ls.flipNotParked(ev.track)

	// An incident-holding node keeps its recorded position and its armed
	// boundaries (ADR-036 §2.4): the node was entered and did not complete, so
	// an SLA timer must keep ticking against it and an interrupting boundary
	// may still overtake it.
	if !incident {
		ls.clearPosition(ev.track)
	}

	// the same withdrawal evEnded performs, for the track that failed rather
	// than ended: its wait is over either way (SRD-071 FR-3b).
	ev.track.releaseHolds()

	if !incident {
		ls.disarmBoundaries(ev.track.ID())
	}
}

// applyMerged flips the tracks the surviving track absorbed at a synchronizing
// join to Merged (their token projects Consumed). It resolves the absorbed ids
// against the loop-owned tracks map; the awaiting goroutines have already
// returned, so the loop is the sole writer of their state. Called only from the
// loop goroutine.
func (ls *loopState) applyMerged(ev trackEvent) {
	survivor := ev.track.ID()

	for _, id := range ev.mergedIDs {
		m := ls.inst.tracks[id]
		if m == nil {
			continue
		}

		m.mergedInto.Store(&survivor)
		m.updateState(TrackMerged)

		// the absorbed track is now dead (Merged) — drop it from the loop-owned position/
		// parked views so it stops counting as occupied or parked (SRD-028 FR-6).
		ls.clearPosition(m)

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
// the loop goroutine on a track end / merge.
func (ls *loopState) recheckAwaitingJoins() {
	seen := map[string]bool{}

	// parked holds exactly the AwaitSync tracks (id → join node), so the loop reads its own
	// view instead of scanning inst.tracks for the state cross-goroutine (SRD-028 FR-3).
	for _, node := range ls.parked {
		if seen[node.ID()] {
			continue
		}

		seen[node.ID()] = true
		ls.recheckJoin(node)
	}
}

// recheckParked handles a track that just parked at a reachability join. If the join
// already fired without recording this track (a late arrival deemed unreachable by an
// earlier fire — FIX-006), the track is a trailing token: consume it (flip to Merged
// and wake it so its goroutine returns). Otherwise recheck the join — a never-taken
// branch may already be unreachable, firing it now with no token death.
func (ls *loopState) recheckParked(t *track) {
	// the join node is the one the track parked on, recorded in the loop-owned parked view
	// by the evParked case — no currentStep cross-read (SRD-028 FR-5).
	node := ls.parked[t.ID()]

	if rj, ok := node.(exec.ReachabilityJoin); ok && rj.IsTrailing(t.ID()) {
		t.updateState(TrackMerged)
		// a trailing token is consumed (Merged) — drop it from the position/parked views so
		// it stops counting, matching today's "Merged ⇒ excluded" (SRD-028 FR-6).
		ls.clearPosition(t)
		t.parkCh <- struct{}{}

		return
	}

	ls.recheckJoin(node)
}

// recheckJoin re-evaluates a parked reachability join (OR-join) against the live
// token positions and fires it when no un-marked incoming flow can still receive
// a token (SRD-022 §2.10), or — for a complex (activation) join — aborts the instance
// when the rule is unsatisfiable (SRD-023). stopAll is the loop's terminate action,
// invoked on an abort so termination does not race the resulting track-end events
// (inst.fail alone only cancels ctx, leaving `stopping` unset). Called only from
// the loop goroutine.
func (ls *loopState) recheckJoin(node flow.Node) {
	// The loop-owned position/parked maps drive BOTH the in-transit guard and the reachability
	// the decision below uses (joinPositions), so they can't disagree — and neither reads a
	// track cross-goroutine (SRD-028 FR-4). An imminent arrival — a live token already on the
	// join node but not yet parked (between its evMoved onto the join and its evParked) — is
	// invisible to the backward reachability (it sits at the excluded join) and is not yet
	// marked. Defer: it re-triggers this recheck via its own evParked once it parks.
	occupied, inTransit := joinPositions(node, ls.position, ls.parked)
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
		dec, err := j.Recheck(ls.inst.guardEval(ls.inst.ctx), fc)

		switch {
		case err != nil:
			ls.inst.fail(err)
			ls.stopAll()

		case dec.Aborted:
			ls.inst.fail(
				errs.New(
					errs.M("complex gateway activation rule is unsatisfiable"),
					errs.C(errorClass, errs.InvalidState),
					errs.D(observability.AttrNodeID, node.ID())))
			ls.stopAll()

		case dec.Fired:
			ls.fireOrJoin(dec.Survivor, dec.Merged)
		}

	case exec.ReachabilityJoin:
		if complete, survivor, merged := j.Recheck(fc); complete {
			ls.fireOrJoin(survivor, merged)
		}
	}
}

// fireOrJoin completes a reachability join: applyMerged flips the absorbed tracks to
// Merged and wakes any that are parked (FR-8 / FIX-006); here we only resume the
// survivor's blocked goroutine into the node. parkCh is buffered(1), so the signal
// never blocks the loop. Called only from the loop goroutine.
func (ls *loopState) fireOrJoin(survivorID string, merged []string) {
	survivor := ls.inst.tracks[survivorID]
	if survivor == nil {
		return
	}

	ls.inst.Logger().Debug("synchronizing join fired",
		observability.AttrInstanceID, ls.inst.ID(),
		observability.AttrNodeID, nodeIDOf(ls.position[survivorID]),
		observability.AttrTrackID, survivorID,
		"merged", len(merged))

	ls.applyMerged(trackEvent{track: survivor, mergedIDs: merged})

	survivor.parkCh <- struct{}{}
}

// applyDehydrated accounts a track that returned in TrackDehydrated (SRD-071
// FR-1): its long-wait goroutine was released. It decrements the active count
// like any terminal event, but — unlike evEnded/evAwaiting — RETAINS every
// piece of the track's bookkeeping (position, waiting/msgIdx registries, scope
// counter): the wait is held externally and a trigger re-materializes the track
// as a continuation fork. The goroutine is gone; the record stays. The track
// argument is the released track (M2's loop-release reads it).
func (ls *loopState) applyDehydrated(_ *track) {
	ls.active--
}

// dehydrateTrack releases a parked track's goroutine (SRD-071 FR-2): it closes
// the track's dehydrateCh, which the parked run() select observes to exit in
// TrackDehydrated. Called only on the loop goroutine. The track record and its
// wait registries are left intact; the goroutine's return emits evDehydrated.
func (ls *loopState) dehydrateTrack(t *track) {
	close(t.dehydrateCh)
}

// maybeDehydrate releases the instance's goroutines when it is FULLY IDLE
// (SRD-071 FR-2): every live track is parked on a Dehydratable wait whose kind
// has an engine holder, no short-lived internal work is outstanding, and the
// SRD-070 capture guards are clear. It flips each parked track to
// TrackDehydrated (releasing its goroutine); the loop drains the resulting
// evDehydrated events to ls.active == 0 and the tail parks the instance. A
// non-armed instance, an un-held or non-dehydratable wait, an executing or
// join-parked track, or an in-flight call/MI/sweep keeps the instance resident.
func (ls *loopState) maybeDehydrate(ctx context.Context) {
	inst := ls.inst
	if inst.cpOwner == "" || ls.dehydrating || ls.stopping ||
		inst.pinnedResident() {
		return
	}

	// the SRD-070 capture guards: a cut can't be taken mid-construct.
	if len(ls.calls) > 0 || len(ls.sweeps) > 0 {
		return
	}

	// An OPEN INCIDENT owns the park (SRD-090.A T-17, ADR-036 §2.2). The
	// loop's tail tries dehydration before parkOnIncidents, so the two exits
	// are not mutually exclusive by position — they were only ever
	// mutually exclusive because a failing track ends TrackIncident, which
	// is absent from liveTrackStates and therefore read as a terminal
	// record, while its composite HOST disqualified the instance through
	// the default arm. FR-8 removes that second half, so the guard has to
	// be stated rather than inherited: an instance waiting for a retry or
	// an operator parks as itself, with the incident records carrying its
	// continuations, and not as Dehydrated.
	if ls.inst.openIncidents() > 0 || ls.inst.deadLetters() > 0 {
		return
	}

	parked := ls.dehydratableParked(ctx)
	if parked == nil {
		return // not fully idle, or nothing to release
	}

	ls.dehydrating = true
	for _, t := range parked {
		ls.dehydrateTrack(t)
	}
}

// dehydrationDetails describes the residency transition for the operator
// (SRD-071 FR-10, ADR-007 v.2 §2.7): WHAT the instance is waiting on and how
// many waits, plus the point of the whole feature — it now holds zero
// goroutines. "How many of my instances are dehydrated, and on what" is the
// question this fact exists to answer, so counting transitions is not enough.
// Names and counts only, never payload.
func (ls *loopState) dehydrationDetails() map[string]string {
	kinds := set.New[string]()
	waits := 0

	for _, t := range ls.inst.tracks {
		if !t.inState(TrackDehydrated) {
			continue
		}

		waits++

		kinds.Add(waitKindOf(t.currentStep().node))
	}

	names := kinds.All()
	sort.Strings(names)

	return map[string]string{
		"wait_kinds": strings.Join(names, ","),
		"waits":      strconv.Itoa(waits),
		"goroutines": "0",
	}
}

// waitKindOf names the kind of wait a node parks on, for the residency facts.
// A node's own trigger names it where there is one; everything else falls back
// to the node's Go type, which reads well enough for an operator (UserTask,
// EventBasedGateway, …).
func waitKindOf(node flow.Node) string {
	if en, ok := node.(flow.EventNode); ok {
		kinds := set.New[string]()
		for _, d := range en.Definitions() {
			kinds.Add(string(d.Type()))
		}

		if names := kinds.All(); len(names) > 0 {
			sort.Strings(names)

			return strings.Join(names, "+")
		}
	}

	return strings.TrimPrefix(fmt.Sprintf("%T", node), "*")
}

// dehydratableParked returns the parked tracks to release when the instance is
// fully idle, or nil if it is not: every live track must be a held, dehydratable
// TrackWaitForEvent (an executing/join-parked track, or an un-held/
// non-dehydratable wait, disqualifies the whole instance).
func (ls *loopState) dehydratableParked(ctx context.Context) []*track {
	inst := ls.inst

	var parked, hosts []*track

	for _, t := range inst.tracks {
		switch {
		case t.inState(TrackWaitForEvent):
			// A track already DISPATCHED to is not idle, even though its state
			// still reads WaitForEvent: the flip to Ready happens on the track's
			// own goroutine when it consumes the event. dispatchToParked clears
			// the waiting entry BEFORE the send, so this is the loop-owned
			// "a trigger is already on its way" signal. Releasing here would
			// leave the track selecting between a closed dehydrateCh and a full
			// evtCh — and a lost trigger every time the former won.
			if _, stillParked := ls.waiting[t.ID()]; !stillParked {
				return nil
			}

			// Every ARMED BOUNDARY guarding this track must be HELD (SRD-071
			// FR-9a, the eligibility half). A boundary watch is not a track,
			// so it never reaches waitReleasable — yet an unheld one dies with
			// the released instance, and "approve within 24h or escalate"
			// would simply never escalate. This is the same per-arm rule an
			// Event-Based Gateway already applies to its arms (FR-3a), at the
			// granularity a boundary needs. The kinds that resolve directly —
			// Error, Escalation, Compensation, Cancel — never enter this map
			// at all (armBoundaries skips them), so they cost no residency; a
			// loop-owned Conditional watch is never holdable and correctly
			// keeps its instance resident.
			if !ls.boundariesHeld(t.ID()) {
				return nil
			}

			if !ls.waitReleasable(ctx, t) {
				return nil
			}

			parked = append(parked, t)

		case t.inState(TrackDehydrated):
			// already released (a partial prior pass) — retained record.

		case !liveTrackStates[t.currentState()]:
			// terminal (Ended/Merged/Canceled/Failed) — a retained record.
			//
			// Asked BEFORE the executor question below, and the order is
			// load-bearing: a track's terminal state is the last word on
			// whether it is doing anything, while its executor answers
			// from the other goroutine and can lag. A host canceled by an
			// interrupting boundary still reports awaitScope until its own
			// goroutine processes the cancel — and releasing it there would
			// record it as dehydrated instead of canceled.

		case t.awaits() == awaitScope:
			// FR-8: a host parked for its body's drain is NOT doing work —
			// the body's own tracks are, and they answer for themselves in
			// the arms above. Before this, such a host fell to the default
			// arm and disqualified its whole instance, so an iterated
			// Sub-Process holding parked User Tasks pinned it for as long
			// as the approvals took — the case ADR-007 v.2.1 exists for.
			//
			// Its boundaries still have to be held, for the same reason a
			// parked track's do: an unheld boundary dies with the released
			// instance, and "approve within 24h or escalate" would never
			// escalate. The guarded activity here is the composite itself.
			if !ls.boundariesHeld(t.ID()) {
				return nil
			}

			// Released, but NOT counted as a reason to release. A host
			// holds no wait — ADR-007 §2.4 is per-WAIT — so nothing
			// external can wake it, and an instance whose only "wait" is
			// a scope host would park on a drain that was about to
			// arrive from inside. It rides along with the body's waits
			// or it does not go at all.
			hosts = append(hosts, t)

		default:
			// live but not a long wait (executing, or a join barrier) — the
			// instance is doing work; stay resident.
			return nil
		}
	}

	// at least one REAL wait, or there is nothing to hydrate this instance
	// back: a released instance wakes on a trigger, and only a held wait
	// has one.
	if len(parked) == 0 {
		return nil
	}

	return append(parked, hosts...)
}

// waitReleasable reports whether a parked track's wait is both eligible
// (its node declares Dehydratable) and wakeable (an engine holder exists).
func (ls *loopState) waitReleasable(ctx context.Context, t *track) bool {
	node := t.currentStep().node

	// the current implementors (UserTask, catches, EBG, ServiceTask) decide
	// from element data alone and ignore re; a data-driven decision that needs
	// it would thread a real environment here.
	d, ok := node.(renv.Dehydratable)
	if !ok || !d.Dehydratable(ctx, nil) {
		return false
	}

	if ls.inst.waitHeld != nil && ls.inst.waitHeld(t) {
		return true
	}

	// The element SAYS it may be released but nothing can wake it (ADR-007 v.2
	// §2.4). Staying resident is the safe answer — never a lost trigger — but
	// it is silent by nature: the instance simply never releases, and an
	// operator has no way to tell a deliberate choice from a missing holder.
	// So say so, once per park, naming the node.
	ls.inst.Logger().Debug(
		"a dehydratable wait has no holder — the instance stays resident",
		observability.AttrInstanceID, ls.inst.ID(),
		observability.AttrTrackID, t.ID(),
		observability.AttrNodeID, node.ID(),
		observability.AttrNodeName, node.Name(),
		"wait_kind", waitKindOf(node))

	return false
}

// failInvariant faults the instance on a broken engine-internal contract and
// stops every track. It returns false so a bool-returning caller can `return
// ls.failInvariant(...)` in one line — the shape the diff-coverage gate excludes
// as unreachable (FIX-034 §3.2.3).
func (ls *loopState) failInvariant(format string, args ...any) bool {
	ls.inst.fail(errs.Invariant(format, args...))
	ls.stopAll()

	return false
}
