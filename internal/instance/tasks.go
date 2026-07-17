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
)

// taskRequest is a Take/Complete operation handed to the instance loop so it runs
// on the single-writer goroutine (ADR-020 §2.4, SRD-034 §4.1). The caller blocks
// on reply for the synchronous verdict.
type taskRequest struct {
	actor   hi.Actor
	reply   chan taskReply
	taskID  string
	outputs []data.Data
	kind    taskReqKind
}

// taskReply is the loop's verdict for a taskRequest.
type taskReply struct {
	err  error
	view interactor.TaskView
}

// taskEntry is the loop-owned registry value for a parked UserTask: its track and
// the UserTask node (carried on evTaskWaiting so the loop never reads the parked
// track's currentStep cross-goroutine).
type taskEntry struct {
	track *track
	node  flow.Node
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
			errs.C(errorClass, errs.InvalidState))
	case <-ctx.Done():
		return interactor.TaskView{}, ctx.Err()
	}

	select {
	case r := <-req.reply:
		return r.view, r.err
	case <-inst.loopDone:
		return interactor.TaskView{}, errs.New(
			errs.M("instance %q stopped before task reply", inst.ID()),
			errs.C(errorClass, errs.InvalidState))
	case <-ctx.Done():
		return interactor.TaskView{}, ctx.Err()
	}
}

// handleTaskRequest services one Take/Complete on the loop goroutine (SRD-034
// §4.1). It resolves the parked task, authorizes the actor over the instance root
// data source, and — for Complete — validates the outputs and delivers a synthetic
// completion to the parked track. All scope access stays on this goroutine.
func (ls *loopState) handleTaskRequest(ctx context.Context, req taskRequest) {
	entry, ok := ls.tasks[req.taskID]
	if !ok {
		req.reply <- taskReply{err: errs.New(
			errs.M("task %q not found or already completed", req.taskID),
			errs.C(errorClass, errs.ObjectNotFound))}

		return
	}

	// entry.node is always a UserTask (only checkNodeType's humanTask branch
	// registers a task), so the assertion cannot fail.
	ht, _ := entry.node.(interactor.HumanTask)

	if err := ls.inst.authorizeTask(ctx, ht, req.actor); err != nil {
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

// authorizeTask runs the task's Authorize over a transient root frame (a
// data.Source resolving process variables) with the engine's expression engine.
func (inst *Instance) authorizeTask(
	ctx context.Context,
	ht interactor.HumanTask,
	actor hi.Actor,
) error {
	frame, err := inst.sc.openFrame("task-authz", ht.ID())
	if err != nil {
		return err
	}
	defer frame.Discard()

	return ht.Authorize(ctx, actor, newExecEnv(inst, frame, nil),
		inst.ExpressionEngine())
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

	// A task in the registry is always still parked: onTaskWaiting adds it to both
	// the registry and the waiting set, and completeTask removes it from both — all
	// on this loop goroutine. So flip it out and deliver on its own evtCh, where the
	// loop is the sole sender and it is parked-and-undelivered (SRD-027). The track
	// wakes, ProcessEvent binds the outputs, Exec advances.
	ls.flipNotParked(entry.track)
	delete(ls.tasks, req.taskID)
	entry.track.evtCh <- interactor.NewTaskCompletion(req.outputs)

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

// addTask records a parked UserTask in the loop-owned registry and announces it
// to the TaskDistributor. Called on the loop goroutine (evTaskWaiting / spawn).
func (ls *loopState) addTask(
	ctx context.Context,
	taskID string,
	tr *track,
	node flow.Node,
) {
	if taskID == "" {
		return // not a human task — nothing to register
	}

	ls.tasks[taskID] = taskEntry{track: tr, node: node}

	dctx, cancel := context.WithTimeout(ctx, distributorTimeout)
	defer cancel()

	inst := ls.inst
	if err := inst.td.Distribute(dctx, inst.buildTaskInfo(taskID, node)); err != nil {
		inst.Logger().Warn("user task distribute failed",
			"instance_id", inst.ID(), "task_id", taskID, "error", err.Error())
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

	ls.waiting[t.ID()] = struct{}{}

	for _, id := range t.msgDefIDs {
		ls.msgIdx[id] = t
	}

	// arm any conditional subscriptions of a born-parked track — the spawn-path
	// twin of onWaiting's arming (SRD-048 FR-7/FR-9); condDefs is
	// construction-immutable, so this read is safe pre-run.
	ls.armConditionals(ctx, t)

	// a track born parked ON a composite (a fork straight into a sub-process,
	// or an initial node that is one) opens its scope from the spawn path —
	// the twin of the mid-run evScopeOpen (SRD-049 FR-8; construction never
	// emits, the SRD-048 deadlock rule).
	node := t.currentStep().node
	if _, isComposite := node.(scopeHost); isComposite {
		ls.onScopeOpen(ctx, t, node)
	}

	// a track born parked ON a Call Activity (a fork straight onto one, or an
	// initial node that is one) launches its child from the spawn path — the
	// twin of the mid-run evCallWaiting (SRD-050 FR-5; construction never emits,
	// the SRD-048 deadlock rule).
	if _, isCall := node.(callActivity); isCall {
		ls.onCallWaiting(ctx, trackEvent{track: t, node: node})
	}

	ls.addTask(ctx, t.taskID, t, node)
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
	ls.addTask(ctx, ev.taskID, ev.track, ev.node)
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
	for id, e := range ls.tasks {
		if e.track == tr {
			delete(ls.tasks, id)
			ls.inst.withdrawTask(ctx, id)
		}
	}
}

// withdrawTask retracts a task from the distributor, logging a distributor error
// without failing the instance.
func (inst *Instance) withdrawTask(ctx context.Context, taskID string) {
	dctx, cancel := context.WithTimeout(ctx, distributorTimeout)
	defer cancel()

	if err := inst.td.Withdraw(dctx, taskID); err != nil {
		inst.Logger().Warn("user task withdraw failed",
			"instance_id", inst.ID(), "task_id", taskID, "error", err.Error())
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
// identity plus the roles that may claim it (no data).
func (inst *Instance) buildTaskInfo(
	taskID string,
	node flow.Node,
) interactor.TaskInfo {
	ht, _ := node.(interactor.HumanTask)

	return interactor.TaskInfo{
		TaskRef: inst.taskRef(taskID, node),
		Roles:   ht.Roles(),
	}
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
