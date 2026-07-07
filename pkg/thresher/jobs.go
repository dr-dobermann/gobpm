package thresher

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
)

// ReportJobCompletion routes a worker's terminal outcome to the instance that
// owns the job and resumes its parked ServiceTask. The owning instance id is
// embedded in the job id (JobID.InstanceID), so no separate registry is needed:
// the engine binds itself as the dispatcher's JobCompletionSink at New, the
// dispatcher calls this from Complete/Fail, and it forwards to the owning
// instance (SRD-036 §4.5). It implements tasks.JobCompletionSink.
func (t *Thresher) ReportJobCompletion(
	ctx context.Context,
	outcome *tasks.WorkerOutcome,
) error {
	if outcome == nil {
		return errs.New(
			errs.M("ReportJobCompletion: a nil outcome isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	inst, err := t.instanceByID(outcome.JobID().InstanceID())
	if err != nil {
		return err
	}

	return inst.ReportJobCompletion(ctx, outcome)
}

// instanceByID resolves a running Instance by its id, or an error if the instance
// is unknown (already completed / terminated).
func (t *Thresher) instanceByID(instanceID string) (*instance.Instance, error) {
	t.m.Lock()
	defer t.m.Unlock()

	reg, ok := t.instances[instanceID]
	if !ok {
		return nil, errs.New(
			errs.M("instance %q not found", instanceID),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	return reg.inst, nil
}
