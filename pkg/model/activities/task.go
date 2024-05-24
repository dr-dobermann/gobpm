package activities

import (
	"context"
	"slices"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// Task is common parent of all Tasks.
type Task struct {
	Activity

	multyInstance bool
}

// NewTask creates a new Task and returns its pointer on success or
// error on failure.
func NewTask(
	name string,
	taskOpts ...options.Option,
) (*Task, error) {
	var (
		actOpts = make([]options.Option, 0, len(taskOpts))
		mInst   = multyInstance(false)
	)

	for _, to := range taskOpts {
		switch o := to.(type) {
		case taskOption:
			err := o.Apply(&mInst)
			if err != nil {
				return nil, err
			}
		default:
			actOpts = append(actOpts, to)
		}
	}

	a, err := NewActivity(name, actOpts...)
	if err != nil {
		return nil, err
	}

	return &Task{
			Activity:      *a,
			multyInstance: bool(mInst)},
		err
}

// IsMultyinstance returns Task multyinstance settings.
func (t *Task) IsMultyinstance() bool {
	return t.multyInstance
}

// --------------------- flow.ActivityNode interface ---------------------------

func (t *Task) ActivityType() flow.ActivityType {
	return flow.TaskActivity
}

// ----------------- scope.NodeDataLoader interface ----------------------------

// RegisterData adds all Task's properties and inputs to the Scope s.
func (t *Task) RegisterData(dp scope.DataPath, s scope.Scope) error {
	t.dataPath = dp

	inputs, err := t.IoSpec.Parameters(data.Input)
	if err != nil {
		return errs.New(
			errs.M("couldn't get task inputs"),
			errs.D("task_name", t.Name()),
			errs.D("task_id", t.Id()),
			errs.E(err))
	}

	dd := make([]data.Data, 0, len(t.properties)+len(inputs))

	for _, p := range t.properties {
		dd = append(dd, p)
	}

	for _, in := range inputs {
		dd = append(dd, in)
	}

	return s.LoadData(t, dd...)
}

// ------------------ scope.NodeDataConsumer interface --------------------------

func (t *Task) LoadData(_ context.Context) error {
	dii, err := t.IoSpec.Parameters(data.Input)
	if err != nil {
		return errs.New(
			errs.M("couldn't get task inputs"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.E(err))
	}

	for _, ia := range t.dataAssociations[data.Input] {
		index := slices.IndexFunc(
			dii,
			func(i *data.Parameter) bool {
				return ia.TargetItemDefId() == i.ItemDefinition().Id()
			})
		if index == -1 {
			return errs.New(
				errs.M("couldn't find task input for association's %q target %q",
					ia.Id(), ia.TargetItemDefId()),
				errs.C(errorClass))
		}

		v, err := ia.Value()
		if err != nil {
			return errs.New(
				errs.M("couldn't get value of the association %q", ia.Id()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}

		if err := dii[index].Subject().Structure().Update(v); err != nil {
			return errs.New(
				errs.M("couldn't update input %q", dii[index].Name()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}
	}

	return nil
}

// ------------------ scope.NodeDataProducer interface --------------------------

func (t *Task) UploadData(_ context.Context) error {
	doo, err := t.IoSpec.Parameters(data.Output)
	if err != nil {
		return errs.New(
			errs.M("couldn't get output parameters for task %q[%s]", t.Name(), t.Id()),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.E(err))
	}

	for _, oa := range t.dataAssociations[data.Output] {
		index := slices.IndexFunc(doo,
			func(o *data.Parameter) bool {
				return oa.HasSourceId(o.Subject().Id())
			})

		if index == -1 {
			return errs.New(
				errs.M("couldn't find task's %q[%s] output for association %q",
					t.Name(), t.Id(), oa.Id()),
				errs.C(errorClass, errs.ObjectNotFound))
		}

		if err := oa.UpdateSource(doo[index].ItemDefinition()); err != nil {
			return errs.New(
				errs.M("couldn't update association's %q source %q for "+
					"task %q[%s]", oa.Id(), doo[index].ItemDefinition().Id(),
					t.Name(), t.Id()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}
	}

	return nil
}

// -----------------------------------------------------------------------------

// interface check
var (
	_ flow.ActivityNode      = (*Task)(nil)
	_ scope.NodeDataLoader   = (*Task)(nil)
	_ scope.NodeDataConsumer = (*Task)(nil)
	_ scope.NodeDataProducer = (*Task)(nil)
)
