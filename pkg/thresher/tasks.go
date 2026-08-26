package thresher

import (
	"context"
	"errors"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// routingDistributor wraps the embedder's TaskDistributor with engine routing: on
// Distribute it records taskID → instanceID (so Take/Complete find the owning
// instance) then forwards; on Withdraw it drops the mapping then forwards. One
// shared instance backs every process instance (SRD-034 §4.5).
type routingDistributor struct {
	thr  *Thresher
	next interactor.TaskDistributor
}

// Distribute records the task's owning instance and its resolved eligibility, then
// forwards the announcement.
func (r *routingDistributor) Distribute(
	ctx context.Context,
	task interactor.TaskInfo,
) error {
	r.thr.registerTask(task)

	return r.next.Distribute(ctx, task)
}

// Withdraw drops the task's routing entry, then forwards the retraction.
func (r *routingDistributor) Withdraw(
	ctx context.Context,
	taskID string,
) error {
	r.thr.unregisterTask(taskID)

	return r.next.Withdraw(ctx, taskID)
}

// taskRecord is the engine-level state of a parked human task: where it lives, who
// may act on it, and who currently holds it. It outlives its instance's residency
// (ADR-020 v.2 §2.1.1), which is what lets the ownership operations answer without
// hydrating anything.
//
// eligible is written once at registration and read-only afterwards; owner is the
// only mutable field, and every mutation happens under Thresher.m (SRD-073 NFR-1).
type taskRecord struct {
	instanceID string
	// owner is the BPMN actualOwner (§10.3.4.1 Table 10.14) — a user-id literal,
	// empty while the task is unowned.
	owner    string
	eligible interactor.Eligibility
}

// registerTask records a distributed task: its owning instance, the triad resolved
// at distribution, and — when the triad names exactly one actor — that actor as the
// initial owner, so a directly-assigned task is born owned (ADR-020 v.2 §2.5.3).
//
// An ALREADY REGISTERED id is a RE-ANNOUNCEMENT, not a new task, and keeps the
// record it already has. Hydrating a released instance re-parks the wait and
// announces it again under the recorded id (ADR-020 §2.1), and both fields must
// survive that:
//
//   - owner, or a claim taken during the wait is wiped by the very Take that opens
//     the form — measured: Claim → Take → Complete refused the holder's own
//     completion as TASK_UNCLAIMED, so §2.4.1's exclusivity lapsed exactly on the
//     long-lived tasks it exists for, and a second candidate could claim behind the
//     first one's back;
//   - eligible, because §2.7 freezes the eligible set at distribution — a set
//     re-resolved on a later announcement is not the set the task was offered on.
//
// Ids come from foundation.GenerateID and a completed or withdrawn task is
// unregistered, so a live id in the map is always the same task — and a rebuild
// restores the instance under its own id, so instanceID cannot have moved either.
// The record is therefore left entirely alone, which keeps owner the only field
// that ever mutates after registration.
func (t *Thresher) registerTask(task interactor.TaskInfo) {
	t.m.Lock()
	defer t.m.Unlock()

	if _, ok := t.tasks[task.TaskID]; ok {
		return
	}

	t.tasks[task.TaskID] = &taskRecord{
		instanceID: task.InstanceID,
		eligible:   task.Eligible,
		owner:      bornOwner(task.Eligible),
	}
}

// bornOwner returns the actor a task is born owned by, or "" when it is born
// unowned. A triad designating exactly one actor has in substance already assigned
// the task: there is no offer to accept and no competing candidate to exclude, so a
// ceremonial self-claim would be a step that can only ever succeed (ADR-020 v.2
// §2.5.3). Several candidates, or none, leave the task awaiting a claim.
func bornOwner(e interactor.Eligibility) string {
	if len(e.Assignee.IDs) == 1 {
		return e.Assignee.IDs[0]
	}

	return ""
}

// unregisterTask drops taskID from the routing registry.
func (t *Thresher) unregisterTask(taskID string) {
	t.m.Lock()
	defer t.m.Unlock()

	delete(t.tasks, taskID)
}

// instanceForTask resolves the running Instance that owns taskID, or an error if
// the task or its instance is unknown (already completed / withdrawn).
func (t *Thresher) instanceForTask(taskID string) (*instance.Instance, error) {
	t.m.Lock()
	defer t.m.Unlock()

	rec, ok := t.tasks[taskID]
	if !ok {
		return nil, errUnknownTask(taskID)
	}

	reg, ok := t.instances[rec.instanceID]
	if !ok {
		return nil, errs.New(
			errs.M("instance %q owning task %q not found",
				rec.instanceID, taskID),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	return reg.inst, nil
}

// errUnknownTask is the not-found verdict for a task id the registry does not hold
// — never distributed, already completed, or withdrawn.
func errUnknownTask(taskID string) error {
	return errs.New(
		errs.M("user task %q not found", taskID),
		errs.C(errorClass, errs.ObjectNotFound))
}

// Take authorizes actor against the parked UserTask taskID and returns its
// TaskView (renderers + data). On an authorization failure it returns the error
// and no data — the task stays parked (ADR-020 §2.3). It routes to the owning
// instance.
func (t *Thresher) Take(
	ctx context.Context,
	taskID string,
	actor hi.Actor,
) (interactor.TaskView, error) {
	var view interactor.TaskView

	err := t.onTaskInstance(ctx, taskID, func(inst *instance.Instance) error {
		var err error
		view, err = inst.Take(ctx, taskID, actor)

		return err
	})

	return view, err
}

// Complete authorizes actor, checks ownership, validates outputs, and — only if all
// three pass — binds the outputs and resumes the parked UserTask taskID. Every
// failure is non-terminal: the task stays parked (ADR-020 v.2 §2.4).
//
// The first two stages run here, against the registry, BEFORE the instance is
// resolved: a wrong-actor or unowned completion is refused without hydrating a
// released instance, which is the common case during a long human wait. The
// instance then re-checks eligibility on the resident path (ADR-020 v.2 §2.4/§5) and
// owns validation, binding and the resume.
func (t *Thresher) Complete(
	ctx context.Context,
	taskID string,
	actor hi.Actor,
	outputs []data.Data,
) error {
	if err := t.gateComplete(taskID, actor); err != nil {
		return err
	}

	return t.onTaskInstance(ctx, taskID, func(inst *instance.Instance) error {
		return inst.Complete(ctx, taskID, actor, outputs)
	})
}

// The refusal classes a completion can carry, so an embedder can tell WHY it was
// refused without parsing a message (ADR-020 v.2 §6). They accompany
// errs.ConditionFailed rather than replacing it, so existing kind checks still hold.
//
// A refusal is never terminal: the task stays parked and the actor (or another) may
// try again once the cause is addressed.
const (
	// TaskUnclaimedClass marks a completion refused because the task is held by
	// nobody — completion is strict, so claiming is a required step, not a
	// courtesy.
	TaskUnclaimedClass = "TASK_UNCLAIMED"

	// TaskNotOwnerClass marks a completion refused because the task is held by a
	// DIFFERENT actor. Distinct from an authorization failure: the actor may well
	// be eligible, and would succeed if the holder released it.
	TaskNotOwnerClass = "TASK_NOT_OWNER"
)

// gateComplete applies ADR-020 v.2 §2.4's first two stages in its own order —
// eligibility, then ownership — over the registry alone, and reports each refusal so
// the reason is observable and not merely returned.
func (t *Thresher) gateComplete(taskID string, actor hi.Actor) error {
	if err := checkTaskActor("Complete", taskID, actor); err != nil {
		return err
	}

	reason, err := t.completeVerdict(taskID, actor)
	if err != nil {
		if reason != "" {
			t.reportTaskOwnership(taskID, observability.PhaseFailed,
				map[string]string{
					observability.AttrUserID: actor.UserID(),
					"reason":                 reason,
				})
		}

		return err
	}

	return nil
}

// completeVerdict renders the pre-hydration verdict and the reason to report it
// under.
//
// The OWNERSHIP decision is taken under the lock, on a freshly read record, so
// the answer cannot be stale by the time it is returned. Eligibility is
// authorized BEFORE that, outside the lock: Authorize is host code — a
// directory or database lookup is the normal implementation — and running it
// under t.m stalled every registration, launch and discovery call in the engine
// behind one embedder call (FIX-038 §1.2). The eligibility policy is written
// once at registration and read-only afterwards, so reading it separately
// cannot go stale.
func (t *Thresher) completeVerdict(
	taskID string,
	actor hi.Actor,
) (string, error) {
	t.m.Lock()
	rec, ok := t.tasks[taskID]

	var eligible interactor.Eligibility
	if ok {
		eligible = rec.eligible
	}
	t.m.Unlock()

	if !ok {
		return "", errUnknownTask(taskID)
	}

	if err := eligible.Authorize(taskID, actor); err != nil {
		return "unauthorized", err
	}

	t.m.Lock()
	defer t.m.Unlock()

	rec, ok = t.tasks[taskID]
	if !ok {
		return "", errUnknownTask(taskID)
	}

	if rec.owner == "" {
		refusal := errs.New(
			errs.M("user task %q is unowned: claim it before completing", taskID),
			errs.C(errorClass, errs.ConditionFailed, TaskUnclaimedClass),
			errs.D(observability.AttrTaskID, taskID),
			errs.D(observability.AttrUserID, actor.UserID()))

		return "unclaimed", refusal
	}

	if rec.owner != actor.UserID() {
		refusal := errs.New(
			errs.M("user task %q is held by another actor", taskID),
			errs.C(errorClass, errs.ConditionFailed, TaskNotOwnerClass),
			errs.D(observability.AttrTaskID, taskID),
			errs.D(observability.AttrUserID, actor.UserID()))

		return "not_owner", refusal
	}

	return "", nil
}

// Claim makes actor the task's actual owner (the BPMN actualOwner, §10.3.4.1
// Table 10.14), so that only actor may complete it (ADR-020 v.2 §2.5.2). It fails
// if the task is already owned — a participant must not seize a colleague's work by
// accident — or if actor is not eligible for it.
//
// The task stays parked and the instance is never hydrated: claiming is a registry
// mutation, not an execution step (ADR-020 v.2 §2.1.1).
func (t *Thresher) Claim(
	_ context.Context,
	taskID string,
	actor hi.Actor,
) error {
	if err := checkTaskActor("Claim", taskID, actor); err != nil {
		return err
	}

	if err := t.setOwner(taskID, actor.UserID(), actor, claimGuard(actor)); err != nil {
		return err
	}

	t.reportTaskOwnership(taskID, observability.PhaseClaimed, map[string]string{
		observability.AttrUserID: actor.UserID(),
	})

	return nil
}

// Unclaim releases actor's hold, returning the task to the eligible pool so any
// eligible actor may claim it again. Only the current owner may unclaim (ADR-020
// v.2 §2.5.2).
func (t *Thresher) Unclaim(
	_ context.Context,
	taskID string,
	actor hi.Actor,
) error {
	if err := checkTaskActor("Unclaim", taskID, actor); err != nil {
		return err
	}

	if err := t.setOwner(taskID, "", actor, ownerOnlyGuard(actor)); err != nil {
		return err
	}

	t.reportTaskOwnership(taskID, observability.PhaseUnclaimed, map[string]string{
		observability.AttrUserID: actor.UserID(),
	})

	return nil
}

// Reassign moves ownership to nomineeUserID, overriding any current owner.
//
// It performs NO check on the caller (ADR-020 v.2 §2.5.2): its callers are managers,
// administrators and offboarding flows, none of which is a participant in the task,
// so gating it on the task's own triad would forbid every legitimate use.
// Authorizing the caller is the embedder's responsibility — it owns identity and
// organizational structure, and should log who invoked this, since the engine
// cannot.
//
// It does check the NOMINEE: a task cannot be handed to someone the process says may
// not perform it. An administrator may choose among eligible actors, never enlarge
// the eligible set. A nominee eligible only through a candidate GROUP cannot be
// nominated, since group membership is authenticated by the embedder for a present
// actor and cannot be asserted for an absent one (SRD-073 §4.4).
func (t *Thresher) Reassign(
	_ context.Context,
	taskID, nomineeUserID string,
) error {
	if err := checkTaskArgs("Reassign", taskID, nomineeUserID); err != nil {
		return err
	}

	var from string

	// The NOMINEE is the actor authorized here, not the caller: a reassignment
	// may only move a task to someone already eligible for it.
	err := t.setOwner(taskID, nomineeUserID, userIDActor(nomineeUserID),
		func(_ string, rec *taskRecord) error {
			from = rec.owner

			return nil
		})
	if err != nil {
		return err
	}

	t.reportTaskOwnership(taskID, observability.PhaseReassigned,
		map[string]string{
			observability.AttrFromUserID: from,
			observability.AttrToUserID:   nomineeUserID,
		})

	return nil
}

// setOwner applies guard to the task's record and, if it passes, writes owner. The
// registry lookup, the guard and the write happen in ONE critical section, so
// concurrent claims on the same task cannot both succeed (SRD-073 NFR-3).
func (t *Thresher) setOwner(
	taskID, owner string,
	actor hi.Actor,
	admit func(string, *taskRecord) error,
) error {
	// PHASE 1 — read the eligibility policy under the lock. It is written once
	// at registration and read-only afterwards (see taskRecord), so one read is
	// enough and it cannot go stale.
	t.m.Lock()

	rec, ok := t.tasks[taskID]
	if !ok {
		t.m.Unlock()

		return errUnknownTask(taskID)
	}

	eligible := rec.eligible

	t.m.Unlock()

	// PHASE 2 — HOST policy, outside the lock. Authorize is embedder code and a
	// directory or database lookup is the normal implementation; running it
	// under t.m stalled every registration, launch and discovery call in the
	// engine behind it (FIX-038 §1.2). This function used to run it inside a
	// `guard` CALLBACK invoked under the lock, which is the shape locked.go
	// forbids by construction — and is why it went unnoticed.
	if err := eligible.Authorize(taskID, actor); err != nil {
		return err
	}

	// PHASE 3 — the ownership decision and the mutation, under the lock and on
	// a FRESHLY read record: the answer must not be stale by the time it is
	// applied, and phase 2 released the lock.
	t.m.Lock()
	defer t.m.Unlock()

	rec, ok = t.tasks[taskID]
	if !ok {
		return errUnknownTask(taskID)
	}

	if err := admit(taskID, rec); err != nil {
		return err
	}

	rec.owner = owner

	return nil
}

// claimGuard admits an eligible actor to a task that is unowned, or that the actor
// already holds.
//
// Claiming a task you already hold is a no-op success, not a failure: the guard
// exists to stop one participant seizing ANOTHER's work, and a same-owner claim
// takes nothing from anybody. Refusing it would also make the operation unsafe to
// retry, and would break every embedder that claims unconditionally before
// completing — including a directly-assigned task, which is born owned (§2.5.3) and
// would otherwise be uncompletable by the very actor the process assigned it to.
// Camunda draws the line the same way: its claim fails only when the existing
// assignee is a different user.
func claimGuard(actor hi.Actor) func(string, *taskRecord) error {
	return func(_ string, rec *taskRecord) error {
		// Eligibility is authorized by setOwner, outside the lock.
		if rec.owner != "" && rec.owner != actor.UserID() {
			return errs.New(
				errs.M("user task is already held by %q", rec.owner),
				errs.C(errorClass, errs.ConditionFailed),
				errs.D("owner", rec.owner),
				errs.D(observability.AttrUserID, actor.UserID()))
		}

		return nil
	}
}

// ownerOnlyGuard admits none but the task's current owner.
func ownerOnlyGuard(actor hi.Actor) func(string, *taskRecord) error {
	return func(_ string, rec *taskRecord) error {
		if rec.owner != actor.UserID() {
			return errs.New(
				errs.M("only the actual owner may release a user task"),
				errs.C(errorClass, errs.ConditionFailed),
				errs.D("owner", rec.owner),
				errs.D(observability.AttrUserID, actor.UserID()))
		}

		return nil
	}
}

// userIDActor adapts a bare user id to an hi.Actor for an eligibility check on an
// ABSENT actor — a reassignment nominee. It reports no groups, because group
// membership is authenticated by the embedder for a present actor and cannot be
// asserted on their behalf (SRD-073 §4.4).
type userIDActor string

func (u userIDActor) UserID() string   { return string(u) }
func (u userIDActor) Groups() []string { return nil }

// reportTaskOwnership emits an ownership transition on the TaskState stream. The
// activity itself stays Active — ownership is an attribute of a parked task, not a
// node phase (ADR-020 v.2 §2.1.1, §2.8).
func (t *Thresher) reportTaskOwnership(
	taskID string,
	phase observability.Phase,
	details map[string]string,
) {
	details[observability.AttrTaskID] = taskID

	t.producer.Report(observability.Fact{
		Kind:    observability.KindTaskState,
		Phase:   phase,
		Details: details,
	})
}

// checkTaskActor validates the public parameters of an actor-driven task operation.
func checkTaskActor(op, taskID string, actor hi.Actor) error {
	if actor == nil {
		return errs.New(
			errs.M("%s: a nil Actor isn't allowed", op),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return checkTaskArgs(op, taskID, actor.UserID())
}

// checkTaskArgs validates a task id and a user id.
func checkTaskArgs(op, taskID, userID string) error {
	if taskID == "" {
		return errs.New(
			errs.M("%s: an empty task id isn't allowed", op),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if userID == "" {
		return errs.New(
			errs.M("%s: an empty user id isn't allowed", op),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D(observability.AttrTaskID, taskID))
	}

	return nil
}

// HoldTask implements exec.WaitHolders (SRD-071 FR-8): accept a parked human
// task as held, so the idle detector may release its instance.
//
// Uniquely among the holders, this one registers NOTHING. A task already lives
// in the distributor's inbox independent of residency (ADR-020), and an action
// on it routes by task id through the distributor's own taskID → instanceID
// entry — which is what hydrates the instance. So there is nothing for a hold
// to own: the answer to "can this wait wake a released instance" is simply yes,
// and the hold is that answer.
//
// It kept a taskID → track registry until SRD-071 M8. No path ever read it and
// nothing deleted it, so it grew by one entry per human task the engine ever
// parked; a registry nothing reads is not bookkeeping, it is a leak with a
// comment (FR-3b).
//
// Declined on a volatile engine: with no checkpoint to wake from, the wait must
// stay resident.
func (t *Thresher) HoldTask(_, _, _ string) error {
	if !t.cfg.repoSet {
		return errNoHold("HoldTask: the engine holds no checkpoints")
	}

	return nil
}

// taskActionAttempts bounds the hydrate-and-replay retry below: one extra round
// absorbs an instance that released its goroutines mid-call.
const taskActionAttempts = 3

// onTaskInstance runs a task action against the instance owning taskID,
// hydrating it when needed and REPLAYING the action if the instance released
// its goroutines underneath the call (SRD-071 FR-8). A human acting on a task
// must not see dehydration at all: the task lives in the distributor's inbox
// regardless of residency, so an action on it always resolves to a definite
// answer — the action's own verdict, never "the instance went away".
func (t *Thresher) onTaskInstance(
	ctx context.Context,
	taskID string,
	action func(*instance.Instance) error,
) error {
	var lastErr error

	for range taskActionAttempts {
		inst, err := t.residentForTask(taskID)
		if err != nil {
			return err
		}

		err = action(inst)

		inst.UnpinResident()

		if !retryAfterHydration(err) {
			return err
		}

		lastErr = err

		// The instance is mid-release: it still reads Active (the Dehydrated
		// state lands at the loop's exit), so re-resolving right now would just
		// find the same dying loop. Wait for it to finish releasing, then the
		// next round hydrates it and replays the action.
		select {
		case <-inst.Done():
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return lastErr
}

// retryAfterHydration reports whether a task action was refused merely because
// the instance was releasing its goroutines, so replaying it is the right move.
func retryAfterHydration(err error) bool {
	if err == nil {
		return false
	}

	var ae *errs.ApplicationError

	return errors.As(err, &ae) && ae.HasClass(instance.TaskRetryClass)
}

// residentForTask resolves the instance owning taskID, HYDRATING it first when
// it has released its goroutines (SRD-071 FR-8), and returns it PINNED — the
// caller must UnpinResident when the action is done.
//
// The pin is what makes this work at all: a parked human task is idle by
// definition, so an instance rebuilt to service an action would release again
// immediately and the action could never win. Pinning suspends that until the
// action has run. A human acting on a task therefore never sees dehydration:
// the task lives in the distributor's inbox either way, re-parks under the SAME
// id (restore preserves it), and the action routes to it normally.
func (t *Thresher) residentForTask(taskID string) (*instance.Instance, error) {
	inst, err := t.instanceForTask(taskID)
	if err != nil {
		return nil, err
	}

	if inst.State() != instance.Dehydrated {
		inst.PinResident()

		return inst, nil
	}

	if hErr := t.hydrateForTask(inst.ID()); hErr != nil {
		return nil, hErr
	}

	// the rebuild re-registered the instance — resolve it afresh. It was built
	// already pinned, so it cannot have released in between.
	rebuilt, err := t.instanceForTask(taskID)
	if err != nil {
		// the pin the rebuild started with would otherwise outlive the failed
		// lookup and hold the instance in memory forever.
		if lost, lostErr := t.instanceByID(inst.ID()); lostErr == nil {
			lost.UnpinResident()
		}

		return nil, err
	}

	return rebuilt, nil
}

// hydrateForTask rebuilds a dehydrated instance so a task action can proceed.
// Trigger-ABSENT (ADR-007 v.2 §2.3): a human task is not an event firing
// through the node, it is a wait the instance must simply be PRESENT for — so
// the woken track re-enters its node and re-parks, and the caller's Take or
// Complete then travels the normal in-instance path. The rebuild starts pinned
// so it cannot release before that action arrives.
func (t *Thresher) hydrateForTask(instanceID string) error {
	// Wait for any in-flight rebuild and take the latch, then rebuild pinned.
	// Returning on a lost claim used to report success without having rebuilt
	// OR pinned, while residentForTask's contract says the instance it hands
	// back "was built already pinned" — so onTaskInstance unpinned a pin this
	// call never took (FIX-037 §1.2).
	if err := t.awaitClaim(instanceID, "hydrateForTask"); err != nil {
		return err
	}

	defer t.releaseWake(instanceID)

	// The wake we waited for may already have made it resident. Pin that
	// instead of rebuilding it again — the pin must exist on EVERY path out of
	// here, because the caller unpins unconditionally.
	if inst, err := t.instanceByID(instanceID); err == nil &&
		inst.State() != instance.Dehydrated {
		inst.PinResident()

		return nil
	}

	return t.rebuildAndContinue(instanceID, nil, instance.WithResidentPin())
}
