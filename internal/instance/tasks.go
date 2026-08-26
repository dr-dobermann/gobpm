package instance

import (
	"context"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// distributorTimeout bounds every TaskDistributor call (Distribute/Withdraw): it
// runs on the instance-loop goroutine, so a slow or hung embedder must not block
// the loop. Distribution is best-effort — a timeout is logged, not fatal.
const distributorTimeout = 5 * time.Second

// taskReqKind selects a human-task operation serviced by the instance loop.
type taskReqKind uint8

const (
	// reqTake authorizes an actor and returns the task's TaskView.
	reqTake taskReqKind = iota
	// reqComplete authorizes, validates the outputs, then resumes the task.
	reqComplete
	// reqActivateAdHoc starts one activity an ad-hoc container has offered
	// (SRD-074 §3.5). It carries no actor authorization of its own — the engine
	// front door authorizes before routing here, as it does for a task action.
	reqActivateAdHoc
	// reqAdHocView reports an ad-hoc container's offered and running activities.
	reqAdHocView
)

// taskRequest is a Take/Complete operation handed to the instance loop so it runs
// on the single-writer goroutine (ADR-020 §2.4, SRD-034 §4.1). The caller blocks
// on reply for the synchronous verdict.
type taskRequest struct {
	actor  hi.Actor
	reply  chan taskReply
	taskID string
	// nodeID names the ad-hoc container an activation or view targets; unused
	// by the task requests, which route by taskID.
	nodeID  string
	outputs []data.Data
	kind    taskReqKind
}

// taskReply is the loop's verdict for a taskRequest.
type taskReply struct {
	err  error
	view interactor.TaskView
	// offered and running answer an ad-hoc view request (SRD-074 §3.5).
	offered []string
	running []string
}

// taskEntry is the loop-owned registry value for a parked UserTask: its track and
// the UserTask node (carried on evTaskWaiting so the loop never reads the parked
// track's currentStep cross-goroutine).
type taskEntry struct {
	track *track
	node  flow.Node

	// eligible is the task's triad resolved when it was distributed (ADR-020 v.2
	// §2.7). Write-once and read-only afterwards, so the engine-level registry may
	// hold the same value without a consistency hazard (SRD-073 FR-5a).
	eligible interactor.Eligibility

	// ord is the instance of an iterated activity this task belongs to
	// (ADR-020 §2.12). N instances of one activity share a track, so the
	// track alone cannot say which of them a completion is for — and
	// delivering to the track would hand it to whichever instance read
	// first. Zero for a lone activity, which has one execution anyway.
	ord int
}

// Take authorizes actor against the parked UserTask taskID and, on success,
// returns its TaskView (renderers + data). On an authorization failure it returns
// the error and no data — the task stays parked (ADR-020 §2.3). Serviced by the
// instance loop.
func (inst *Instance) Take(
	ctx context.Context,
	taskID string,
	actor hi.Actor,
) (interactor.TaskView, error) {
	if err := checkTaskArgs(taskID, actor); err != nil {
		return interactor.TaskView{}, err
	}

	return inst.taskRoundtrip(ctx,
		taskRequest{kind: reqTake, taskID: taskID, actor: actor})
}

// Complete authorizes actor, validates outputs against the task's output spec,
// and — only if both pass — binds the outputs and resumes the parked token. An
// authorization or validation failure is non-terminal: the task stays parked and
// waits for the right actor / corrected outputs (ADR-020 §2.4). Serviced by the
// instance loop.
func (inst *Instance) Complete(
	ctx context.Context,
	taskID string,
	actor hi.Actor,
	outputs []data.Data,
) error {
	if err := checkTaskArgs(taskID, actor); err != nil {
		return err
	}

	_, err := inst.taskRoundtrip(ctx,
		taskRequest{
			kind: reqComplete, taskID: taskID, actor: actor, outputs: outputs,
		})

	return err
}

// checkTaskArgs validates the public Take/Complete parameters.
func checkTaskArgs(taskID string, actor hi.Actor) error {
	if taskID == "" {
		return errs.New(
			errs.M("an empty task id isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if actor == nil {
		return errs.New(
			errs.M("a nil Actor isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return nil
}

// TaskRetryClass marks a task action refused because the instance is RELEASING
// (or has released) its goroutines rather than because the action was invalid
// (SRD-071 FR-8). It is the engine's signal to hydrate the instance and replay
// the action — never surfaced to the actor, whose Take/Complete simply succeeds
// against the rebuilt instance.
const TaskRetryClass = "TASK_RETRY_AFTER_HYDRATION"

// taskRoundtrip hands req to the loop and blocks for the reply, honoring ctx and
// instance shutdown.
func (inst *Instance) taskRoundtrip(
	ctx context.Context,
	req taskRequest,
) (interactor.TaskView, error) {
	req.reply = make(chan taskReply, 1)

	select {
	case inst.taskReq <- req:
	case <-inst.loopDone:
		return interactor.TaskView{}, errs.New(
			errs.M("instance %q is not running", inst.ID()),
			errs.C(errorClass, errs.InvalidState, TaskRetryClass))
	case <-ctx.Done():
		return interactor.TaskView{}, ctx.Err()
	}

	select {
	case r := <-req.reply:
		return r.view, r.err
	case <-inst.loopDone:
		return interactor.TaskView{}, errs.New(
			errs.M("instance %q stopped before task reply", inst.ID()),
			errs.C(errorClass, errs.InvalidState, TaskRetryClass))
	case <-ctx.Done():
		return interactor.TaskView{}, ctx.Err()
	}
}

// handleTaskRequest services one Take/Complete on the loop goroutine (SRD-034
// §4.1). It resolves the parked task, authorizes the actor over the instance root
// data source, and — for Complete — validates the outputs and delivers a synthetic
// completion to the parked track. All scope access stays on this goroutine.
func (ls *loopState) handleTaskRequest(ctx context.Context, req taskRequest) {
	// The loop has committed to releasing its goroutines (SRD-071 FR-8): the
	// parked track is already gone or going, so completing here would post the
	// outcome into an evtCh nobody reads and lose it. Refuse with the retry
	// class instead — the engine hydrates the instance and replays the action
	// against the rebuilt one, where the task is parked again under the same id.
	if ls.dehydrating {
		req.reply <- taskReply{err: errs.New(
			errs.M("instance %q is releasing its goroutines", ls.inst.ID()),
			errs.C(errorClass, TaskRetryClass))}

		return
	}

	// The ad-hoc requests route by container node, not by task id, so they are
	// serviced before the task registry lookup below.
	switch req.kind {
	case reqActivateAdHoc:
		req.reply <- taskReply{
			err: ls.activateAdHoc(ctx, req.nodeID, req.taskID)}

		return

	case reqAdHocView:
		offered, running, err := ls.adHocView(req.nodeID)
		req.reply <- taskReply{offered: offered, running: running, err: err}

		return
	}

	entry, ok := ls.tasks[req.taskID]
	if !ok {
		req.reply <- taskReply{err: errs.New(
			errs.M("task %q not found or already completed", req.taskID),
			errs.C(errorClass, errs.ObjectNotFound))}

		return
	}

	// Eligibility was resolved once, at distribution (ADR-020 v.2 §2.7); this is a
	// membership test over that frozen snapshot, so it needs no scope access and
	// cannot disagree with the verdict a claim was granted under.
	if err := entry.eligible.Authorize(req.taskID, req.actor); err != nil {
		req.reply <- taskReply{err: err} // non-terminal — task stays parked

		return
	}

	if req.kind == reqTake {
		// The actor took the task for work (SRD-041 §3.4).
		ls.inst.report(observability.Fact{
			Kind:     observability.KindTaskState,
			Phase:    observability.PhaseTaken,
			NodeID:   entry.node.ID(),
			NodeName: entry.node.Name(),
			Details:  map[string]string{observability.AttrTaskID: req.taskID},
		})

		req.reply <- taskReply{view: ls.inst.buildTaskView(req.taskID, entry.node)}

		return
	}

	ls.completeTask(ctx, req, entry)
}

// completeTask validates the outputs and, on success, resumes the parked task by
// delivering a synthetic completion event to its evtCh, then withdraws it.
func (ls *loopState) completeTask(
	ctx context.Context,
	req taskRequest,
	entry taskEntry,
) {
	ht, _ := entry.node.(interactor.HumanTask)

	if err := ht.ValidateOutputs(req.outputs); err != nil {
		req.reply <- taskReply{err: err} // non-terminal — task stays parked

		return
	}

	// Record WHO performed the work before resuming. It is written here, past every
	// rejectable stage, so only an accepted completion leaves a record (ADR-020 v.2
	// §2.4.2).
	ls.inst.recordCompletedBy(entry.node, req.actor)

	// A task in the registry is always still parked: onTaskWaiting adds it to both
	// the registry and the waiting set, and completeTask removes it from both — all
	// on this loop goroutine. So flip it out and deliver on its own evtCh, where the
	// loop is the sole sender and it is parked-and-undelivered (SRD-027). The track
	// wakes, ProcessEvent binds the outputs, Exec advances.
	delete(ls.tasks, req.taskID)
	ls.deliverCompletion(entry, interactor.NewTaskCompletion(req.outputs))

	// The actor completed the task; the parked track resumes (SRD-041 §3.4).
	// The following withdrawTask additionally emits Withdrawn — the distributor
	// retraction is a distinct fact from the lifecycle completion.
	ls.inst.report(observability.Fact{
		Kind:     observability.KindTaskState,
		Phase:    observability.PhaseCompleted,
		NodeID:   entry.node.ID(),
		NodeName: entry.node.Name(),
		Details:  map[string]string{observability.AttrTaskID: req.taskID},
	})

	ls.inst.withdrawTask(ctx, req.taskID)

	req.reply <- taskReply{}
}

// deliverCompletion hands an accepted completion to the execution that owns the
// task, and takes the track out of the parked set only when nothing else does.
//
// Both halves matter once an activity can hold N tasks at once (ADR-020 §2.12):
//
//   - delivering on the TRACK's channel would hand the outcome to whichever
//     instance happened to read first, which is not necessarily the one whose
//     task was completed;
//   - flipping the track out on the FIRST completion would drop every
//     sibling's delivery at the dispatch gate, because a track that is no
//     longer parked is no longer a delivery target.
//
// A lone activity has one instance and no decorator, so it keeps the track's
// own channel and flips out immediately — the path is unchanged for it.
func (ls *loopState) deliverCompletion(
	entry taskEntry, completion flow.EventDefinition,
) {
	owner := entry.track.activityOwner()
	if owner == nil {
		ls.flipNotParked(entry.track)
		entry.track.evtCh <- completion

		return
	}

	// this instance's work is done: its identity names nothing from here, and
	// a later pass of the same activity mints its own.
	owner.dropTaskID(entry.ord)
	owner.unparked(entry.ord)

	if box := owner.boxFor(entry.ord); box != nil {
		box <- completion
	}

	// the track leaves the parked set only when NO instance still holds work.
	if !owner.anyWaiting() {
		ls.flipNotParked(entry.track)
	}
}

// recordCompletedBy notes the completing actor in the instance's performer register,
// so the record outlives the task and any later node can read who did the work — the
// approver-is-the-performer's-manager pattern (ADR-020 v.2 §2.4.2).
//
// The register is served through the reserved read-only RUNTIME subtree rather than
// committed into the data plane, because the record is engine-published: a process
// must be able to READ who performed a task and must not be able to overwrite the
// record, nor collide with it by naming a variable the same way. It is engine-written
// rather than taken from the submitted outputs for the same reason — a self-reported
// performer identity is the one field a caller must not supply.
//
// Recording is a guarded map write, so unlike a data-plane commit it cannot fail;
// there is no failure mode to report here.
func (inst *Instance) recordCompletedBy(node flow.Node, actor hi.Actor) {
	inst.performers.record(performerKey(node), actor.UserID())
}

// performerKey names a node in the performer register. A node's name is the handle a
// modeler writing an expression has, so it is preferred; an unnamed node falls back
// to its id, which is unlovely but always unique and stable.
func performerKey(node flow.Node) string {
	if name := node.Name(); name != "" {
		return name
	}

	return node.ID()
}

// addTask records a parked UserTask in the loop-owned registry and announces it
// to the TaskDistributor. Called on the loop goroutine (evTaskWaiting / spawn).
func (ls *loopState) addTask(
	ctx context.Context,
	taskID string,
	tr *track,
	node flow.Node,
	ord int,
) {
	if taskID == "" {
		return // not a human task — nothing to register
	}

	inst := ls.inst

	// Resolve the triad once, here, and keep the snapshot on the registry entry:
	// every later check reads it instead of re-resolving (ADR-020 v.2 §2.7).
	info := inst.buildTaskInfo(ctx, taskID, node)

	ls.tasks[taskID] = taskEntry{
		track:    tr,
		node:     node,
		ord:      ord,
		eligible: info.Eligible,
	}

	dctx, cancel := context.WithTimeout(ctx, distributorTimeout)
	defer cancel()

	if err := inst.td.Distribute(dctx, info); err != nil {
		inst.Logger().Warn("user task distribute failed",
			observability.AttrInstanceID, inst.ID(), observability.AttrTaskID, taskID, observability.AttrError, err.Error())
	}

	// The task is parked and announced to the distributor (SRD-041 §3.4).
	inst.report(observability.Fact{
		Kind:     observability.KindTaskState,
		Phase:    observability.PhaseAnnounced,
		NodeID:   node.ID(),
		NodeName: node.Name(),
		Details:  map[string]string{observability.AttrTaskID: taskID},
	})
}

// recordBornWaiter registers a track that begins already parked (a wait node or
// UserTask reached as the track's initial node), on the loop goroutine before the
// track's run goroutine starts: it enters the parked set, indexes its Message
// catch defs, and — for a UserTask — registers and announces the task (SRD-027
// FR-5, SRD-034). A non-waiting track is a no-op.
func (ls *loopState) recordBornWaiter(ctx context.Context, t *track) {
	if !t.inState(TrackWaitForEvent) {
		return
	}

	// a continuation-fork wake (SRD-071 FR-4) re-enters the wait node with its
	// trigger already in evtCh: it fires through, it does not wait — so it is
	// never registered as a waiter (no hub re-registration, no ls.waiting, no
	// conditional re-arm). run() reads the preloaded trigger and delivers.
	if t.woken {
		return
	}

	ls.waiting[t.ID()] = struct{}{}

	for _, id := range t.msgDefIDs {
		ls.addMsgSub(id, t)
	}

	// arm any conditional subscriptions of a born-parked track — the spawn-path
	// twin of onWaiting's arming (SRD-048 FR-7/FR-9); condDefs is
	// construction-immutable, so this read is safe pre-run.
	ls.armConditionals(ctx, t)

	// A composite is deliberately absent here (SRD-090.A M3c). A track born
	// on one used to be born PARKED, so the spawn path had to open its scope
	// on the loop goroutine — construction cannot emit, the SRD-048 deadlock
	// rule. It is no longer a wait at all: the track starts Ready, reaches
	// its step on its own goroutine, and its executor requests the open
	// through the ordinary roundtrip. Nothing is owed here, and this guard
	// never fires for it because recordBornWaiter returns early on a track
	// that is not parked.
	node := t.currentStep().node

	// a track born parked ON a Call Activity (a fork straight onto one, or an
	// initial node that is one) launches its child from the spawn path — the
	// twin of the mid-run evCallWaiting (SRD-050 FR-5; construction never emits,
	// the SRD-048 deadlock rule).
	if _, isCall := node.(callActivity); isCall {
		// a RESTORED caller re-parks WITHOUT re-invoking (SRD-082
		// FR-7): the adoption re-linked it to the recorded child — a
		// second InvokeProcess would duplicate the child instance.
		if t.callRestored {
			t.callRestored = false
		} else {
			ls.onCallWaiting(ctx, trackEvent{track: t, node: node})
		}
	}

	// a track born parked ON a wait-for-completion Compensation throw (a fork
	// straight onto one — e.g. an exception flow leading directly to it)
	// starts its sweep from the spawn path — the twin of the mid-run
	// evCompensate (SRD-059 FR-5; construction never emits).
	if tw, ok := node.(interface{ CompensationWaitRef() (string, bool) }); ok {
		if ref, wait := tw.CompensationWaitRef(); wait {
			// a RESTORED thrower re-parks WITHOUT re-throwing (SRD-082
			// FR-6): its adopted sweep is already running and delivers
			// the sentinel — a re-throw would sweep the already-consumed
			// ledger and resume the thrower early.
			if t.compWaitRestored {
				t.compWaitRestored = false
			} else {
				ls.applyCompensate(ctx, trackEvent{
					track:    t,
					node:     node,
					compRef:  ref,
					compWait: true,
				})
			}
		}
	}

	// Only a HUMAN task has a parked-work identity. Every other born waiter —
	// an event catch, a compensation throw — parks on a subscription and must
	// not be registered as a task; asking for an identity here would mint one
	// for it and announce a task nobody modeled.
	if _, human := node.(interactor.HumanTask); !human {
		return
	}

	// the same identity rule as a mid-run park: an ITERATED activity's task
	// belongs to the instance, a lone one to the track (ADR-020 §2.12). A
	// restored execution adopts its recorded id inside, so a rehydrated task
	// keeps the handle its inbox entry carries.
	// no executor here: a born-parked waiter is the track's own, and a
	// restored fan-out re-classifies per instance when its decorator runs.
	taskID, ord := t.humanTaskIdentity(nil)

	ls.addTask(ctx, taskID, t, node, ord)
}

// onTaskWaiting records a parked UserTask and announces it to the distributor,
// unless the instance is shutting down (a parked task is then torn down by
// stopAll, not completed). It also marks the track parked-and-undelivered so a
// Complete can deliver to it. Runs on the loop goroutine (SRD-034).
func (ls *loopState) onTaskWaiting(ctx context.Context, ev trackEvent) {
	if ls.stopping {
		return
	}

	ls.waiting[ev.track.ID()] = struct{}{}
	ls.addTask(ctx, ev.taskID, ev.track, ev.node, ev.ord)
}

// withdrawAllTasks withdraws every parked task and clears the registry, used on
// instance teardown when tasks are no longer completable (SRD-034). A fresh
// context is used since the instance context is already canceled at that point.
func (ls *loopState) withdrawAllTasks() {
	for id := range ls.tasks {
		ls.inst.withdrawTask(context.Background(), id)
	}

	clear(ls.tasks)
}

// cleanupTask withdraws and drops any task owned by a track that ended without a
// normal completion (canceled by an interrupting boundary or instance terminate).
func (ls *loopState) cleanupTask(ctx context.Context, tr *track) {
	// the entry is read by one field only, so range over keys: copying the whole
	// value per iteration grew past gocritic's threshold when Eligibility gained
	// its role slot (SRD-075).
	for id := range ls.tasks {
		if ls.tasks[id].track != tr {
			continue
		}

		delete(ls.tasks, id)
		ls.inst.withdrawTask(ctx, id)
	}
}

// withdrawTask retracts a task from the distributor, logging a distributor error
// without failing the instance.
func (inst *Instance) withdrawTask(ctx context.Context, taskID string) {
	dctx, cancel := context.WithTimeout(ctx, distributorTimeout)
	defer cancel()

	if err := inst.td.Withdraw(dctx, taskID); err != nil {
		inst.Logger().Warn("user task withdraw failed",
			observability.AttrInstanceID, inst.ID(), observability.AttrTaskID, taskID, observability.AttrError, err.Error())
	}

	// The task was retracted from the distributor (SRD-041 §3.4) — on completion,
	// cancellation, or instance teardown.
	inst.report(observability.Fact{
		Kind:    observability.KindTaskState,
		Phase:   observability.PhaseWithdrawn,
		Details: map[string]string{observability.AttrTaskID: taskID},
	})
}

// buildTaskInfo builds the pre-authorization announcement for a parked UserTask:
// identity, the roles that may claim it, and the triad resolved to identifier sets
// (no data). Resolving here is the single resolution point of ADR-020 v.2 §2.7 —
// this runs on the loop goroutine while the instance is still resident, which is
// the last moment the task's expressions can read process data.
func (inst *Instance) buildTaskInfo(
	ctx context.Context,
	taskID string,
	node flow.Node,
) interactor.TaskInfo {
	ht, _ := node.(interactor.HumanTask)

	return interactor.TaskInfo{
		TaskRef:  inst.taskRef(taskID, node),
		Roles:    ht.Roles(),
		Eligible: inst.resolveEligibility(ctx, taskID, node),
		Priority: ht.TaskPriority(),
	}
}

// resolveEligibility resolves a task's triad over a transient root frame (a
// data.Source exposing the instance's process variables) with the engine's
// expression engine — the single resolution point of ADR-020 v.2 §2.7.
//
// A frame that cannot be opened fails **closed**: it returns an Eligibility that
// authorizes nobody, never the zero value (which would read as an open task and
// silently authorize everyone). The task then stays parked and uncompletable, which
// is recoverable and visible; the alternative is a silent authorization bypass. The
// failure is both logged and reported as a fact, so it is never dropped quietly.
func (inst *Instance) resolveEligibility(
	ctx context.Context,
	taskID string,
	node flow.Node,
) interactor.Eligibility {
	ht, _ := node.(interactor.HumanTask)

	frame, err := inst.sc.openFrame("task-eligibility", ht.ID())
	if err != nil {
		return inst.denyByResolutionFailure(taskID, node, err)
	}

	defer frame.Discard()

	return ht.ResolveEligibility(
		ctx, newExecEnv(inst, frame, nil), inst.ExpressionEngine())
}

// denyByResolutionFailure turns an unresolvable triad into a fail-closed verdict:
// an Eligibility that authorizes nobody, plus a logged warning and an observable
// fact naming the cause and its effect.
//
// It exists as its own function because the mapping is the security-relevant part —
// the zero Eligibility would read as an OPEN task and silently authorize every
// actor, so "resolution failed" must never be allowed to mean "anyone may act".
// Naming it keeps that rule provable in isolation, independent of how a frame comes
// to fail (FR-5e, §4.8).
func (inst *Instance) denyByResolutionFailure(
	taskID string,
	node flow.Node,
	cause error,
) interactor.Eligibility {
	inst.Logger().Warn("user task eligibility resolution failed",
		observability.AttrInstanceID, inst.ID(), observability.AttrTaskID, taskID, observability.AttrError, cause.Error())

	inst.report(observability.Fact{
		Kind:     observability.KindTaskState,
		Phase:    observability.PhaseFailed,
		NodeID:   node.ID(),
		NodeName: node.Name(),
		Details: map[string]string{
			observability.AttrTaskID: taskID,
			"reason":                 "eligibility_resolution_failed",
			"effect":                 "denied_all_actors",
		},
	})

	return interactor.DeniedEligibility()
}

// buildTaskView builds the post-authorization snapshot: the renderers and the
// self-describing data (the task's properties, e.g. a FORM_ID convention).
func (inst *Instance) buildTaskView(
	taskID string,
	node flow.Node,
) interactor.TaskView {
	ht, _ := node.(interactor.HumanTask)

	props := ht.Properties()
	dd := make([]data.Data, 0, len(props))
	for _, p := range props {
		dd = append(dd, p)
	}

	return interactor.TaskView{
		TaskRef:   inst.taskRef(taskID, node),
		Renderers: ht.Renderers(),
		Data:      dd,
	}
}

// taskRef assembles the shared identity for a task's TaskInfo/TaskView.
func (inst *Instance) taskRef(taskID string, node flow.Node) interactor.TaskRef {
	return interactor.TaskRef{
		TaskID:     taskID,
		InstanceID: inst.ID(),
		NodeID:     node.ID(),
		ProcessID:  inst.s.ProcessID,
	}
}
