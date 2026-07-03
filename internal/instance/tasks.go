package instance

import (
	"context"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
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
func (inst *Instance) handleTaskRequest(
	ctx context.Context,
	req taskRequest,
	tasks map[string]taskEntry,
	waiting map[string]struct{},
	msgIdx map[string]*track,
) {
	entry, ok := tasks[req.taskID]
	if !ok {
		req.reply <- taskReply{err: errs.New(
			errs.M("task %q not found or already completed", req.taskID),
			errs.C(errorClass, errs.ObjectNotFound))}

		return
	}

	// entry.node is always a UserTask (only checkNodeType's humanTask branch
	// registers a task), so the assertion cannot fail.
	ht, _ := entry.node.(interactor.HumanTask)

	if err := inst.authorizeTask(ctx, ht, req.actor); err != nil {
		req.reply <- taskReply{err: err} // non-terminal — task stays parked

		return
	}

	if req.kind == reqTake {
		req.reply <- taskReply{view: inst.buildTaskView(req.taskID, entry.node)}

		return
	}

	inst.completeTask(ctx, req, entry, tasks, waiting, msgIdx)
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

	return ht.Authorize(ctx, actor, newExecEnv(inst, frame),
		inst.ExpressionEngine())
}

// completeTask validates the outputs and, on success, resumes the parked task by
// delivering a synthetic completion event to its evtCh, then withdraws it.
func (inst *Instance) completeTask(
	ctx context.Context,
	req taskRequest,
	entry taskEntry,
	tasks map[string]taskEntry,
	waiting map[string]struct{},
	msgIdx map[string]*track,
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
	flipNotParked(entry.track, waiting, msgIdx)
	delete(tasks, req.taskID)
	entry.track.evtCh <- interactor.NewTaskCompletion(req.outputs)

	inst.withdrawTask(ctx, req.taskID)

	req.reply <- taskReply{}
}

// addTask records a parked UserTask in the loop-owned registry and announces it
// to the TaskDistributor. Called on the loop goroutine (evTaskWaiting / spawn).
func (inst *Instance) addTask(
	ctx context.Context,
	taskID string,
	tr *track,
	node flow.Node,
	tasks map[string]taskEntry,
) {
	if taskID == "" {
		return // not a human task — nothing to register
	}

	tasks[taskID] = taskEntry{track: tr, node: node}

	dctx, cancel := context.WithTimeout(ctx, distributorTimeout)
	defer cancel()

	if err := inst.td.Distribute(dctx, inst.buildTaskInfo(taskID, node)); err != nil {
		inst.Logger().Warn("user task distribute failed",
			"instance", inst.ID(), "task_id", taskID, "error", err.Error())
	}
}

// recordBornWaiter registers a track that begins already parked (a wait node or
// UserTask reached as the track's initial node), on the loop goroutine before the
// track's run goroutine starts: it enters the parked set, indexes its Message
// catch defs, and — for a UserTask — registers and announces the task (SRD-027
// FR-5, SRD-034). A non-waiting track is a no-op.
func (inst *Instance) recordBornWaiter(
	ctx context.Context,
	t *track,
	waiting map[string]struct{},
	msgIdx map[string]*track,
	tasks map[string]taskEntry,
) {
	if !t.inState(TrackWaitForEvent) {
		return
	}

	waiting[t.ID()] = struct{}{}

	for _, id := range t.msgDefIDs {
		msgIdx[id] = t
	}

	inst.addTask(ctx, t.taskID, t, t.currentStep().node, tasks)
}

// onTaskWaiting records a parked UserTask and announces it to the distributor,
// unless the instance is shutting down (a parked task is then torn down by
// stopAll, not completed). It also marks the track parked-and-undelivered so a
// Complete can deliver to it. Runs on the loop goroutine (SRD-034).
func (inst *Instance) onTaskWaiting(
	ctx context.Context,
	ev trackEvent,
	stopping bool,
	waiting map[string]struct{},
	tasks map[string]taskEntry,
) {
	if stopping {
		return
	}

	waiting[ev.track.ID()] = struct{}{}
	inst.addTask(ctx, ev.taskID, ev.track, ev.node, tasks)
}

// withdrawAllTasks withdraws every parked task and clears the registry, used on
// instance teardown when tasks are no longer completable (SRD-034). A fresh
// context is used since the instance context is already canceled at that point.
func (inst *Instance) withdrawAllTasks(tasks map[string]taskEntry) {
	for id := range tasks {
		inst.withdrawTask(context.Background(), id)
	}

	clear(tasks)
}

// cleanupTask withdraws and drops any task owned by a track that ended without a
// normal completion (canceled by an interrupting boundary or instance terminate).
func (inst *Instance) cleanupTask(
	ctx context.Context,
	tr *track,
	tasks map[string]taskEntry,
) {
	for id, e := range tasks {
		if e.track == tr {
			delete(tasks, id)
			inst.withdrawTask(ctx, id)
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
			"instance", inst.ID(), "task_id", taskID, "error", err.Error())
	}
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
