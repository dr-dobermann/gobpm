package thresher

import (
	"context"
	"errors"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
)

// routingDistributor wraps the embedder's TaskDistributor with engine routing: on
// Distribute it records taskID → instanceID (so Take/Complete find the owning
// instance) then forwards; on Withdraw it drops the mapping then forwards. One
// shared instance backs every process instance (SRD-034 §4.5).
type routingDistributor struct {
	thr  *Thresher
	next interactor.TaskDistributor
}

// Distribute records the task's owning instance, then forwards the announcement.
func (r *routingDistributor) Distribute(
	ctx context.Context,
	task interactor.TaskInfo,
) error {
	r.thr.registerTask(task.TaskID, task.InstanceID)

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

// registerTask records that taskID lives on instanceID.
func (t *Thresher) registerTask(taskID, instanceID string) {
	t.m.Lock()
	defer t.m.Unlock()

	t.tasks[taskID] = instanceID
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

	instID, ok := t.tasks[taskID]
	if !ok {
		return nil, errs.New(
			errs.M("user task %q not found", taskID),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	reg, ok := t.instances[instID]
	if !ok {
		return nil, errs.New(
			errs.M("instance %q owning task %q not found", instID, taskID),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	return reg.inst, nil
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

// Complete authorizes actor, validates outputs, and — only if both pass — binds
// the outputs and resumes the parked UserTask taskID. An authorization or
// validation failure is non-terminal: the task stays parked (ADR-020 §2.4). It
// routes to the owning instance.
func (t *Thresher) Complete(
	ctx context.Context,
	taskID string,
	actor hi.Actor,
	outputs []data.Data,
) error {
	return t.onTaskInstance(ctx, taskID, func(inst *instance.Instance) error {
		return inst.Complete(ctx, taskID, actor, outputs)
	})
}

// HoldTask implements exec.WaitHolders (SRD-071 FR-8): record which TRACK a
// parked human task belongs to, so an action on that task can wake a released
// instance. Nothing is subscribed — the task itself already lives in the
// distributor's inbox, independent of residency (ADR-020) — so the hold is pure
// bookkeeping alongside the taskID → instanceID routing entry the distributor
// records. Declined on a volatile engine: with no checkpoint to wake from, the
// wait must stay resident.
func (t *Thresher) HoldTask(instanceID, trackID, taskID string) error {
	if !t.cfg.repoSet {
		return errNoHold("HoldTask: the engine holds no checkpoints")
	}

	t.m.Lock()
	defer t.m.Unlock()

	t.taskTracks[taskID] = taskHold{instanceID: instanceID, trackID: trackID}

	return nil
}

// taskHold locates the parked track a held human task belongs to.
type taskHold struct {
	instanceID string
	trackID    string
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
	if !t.claimWake(instanceID) {
		return nil // another wake is already rebuilding it
	}
	defer t.releaseWake(instanceID)

	return t.rebuildAndContinue(instanceID, nil, instance.WithResidentPin())
}
