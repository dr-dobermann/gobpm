package thresher

import (
	"context"

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
	inst, err := t.instanceForTask(taskID)
	if err != nil {
		return interactor.TaskView{}, err
	}

	return inst.Take(ctx, taskID, actor)
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
	inst, err := t.instanceForTask(taskID)
	if err != nil {
		return err
	}

	return inst.Complete(ctx, taskID, actor, outputs)
}
