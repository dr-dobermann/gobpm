package activities

import (
	"context"
	"fmt"
	"slices"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// task is common parent of all Tasks.
type task struct {
	activity

	multyInstance bool
}

// newTask creates a new Task and returns its pointer on success or
// error on failure.
func newTask(
	name string,
	taskOpts ...options.Option,
) (*task, error) {
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

	a, err := newActivity(name, actOpts...)
	if err != nil {
		return nil, err
	}

	return &task{
			activity:      *a,
			multyInstance: bool(mInst),
		},
		err
}

// IsMultyinstance returns Task multyinstance settings.
func (t *task) IsMultyinstance() bool {
	return t.multyInstance
}

// --------------------- flow.ActivityNode interface ---------------------------

func (t *task) ActivityType() flow.ActivityType {
	return flow.TaskActivity
}

// ------------------ scope.NodeDataConsumer interface --------------------------

// LoadData loads data from Task's incoming data associations into its
// inputs.
func (t *task) LoadData(ctx context.Context) error {
	dii, err := t.IoSpec.Parameters(data.Input)
	if err != nil {
		return errs.New(
			errs.M("couldn't get task inputs"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("task_name", t.Name()),
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
				errs.C(errorClass),
				errs.D("task_name", t.Name()))
		}

		v, err := ia.Value(ctx)
		if err != nil {
			return errs.New(
				errs.M("couldn't get value of the association %q", ia.Id()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}

		if err := dii[index].Subject().Structure().Update(ctx, v.Structure().Get(ctx)); err != nil {
			return errs.New(
				errs.M("couldn't update input %q", dii[index].Name()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}
	}

	return nil
}

// ----------------- scope.NodeDataLoader interface ----------------------------

// RegisterData adds all Task's properties and inputs to the Scope s.
func (t *task) RegisterData(dp scope.DataPath, s scope.Scope) error {
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

// ------------------ scope.NodeDataProducer interface --------------------------

// UploadData fills all Task's outputs with not-Ready state from the Scope and
// loads all Task's outgoing data associations from Task's outputs.
func (t *task) UploadData(ctx context.Context, s scope.Scope) error {
	doo, err := t.updateOutputs(ctx, s)
	if err != nil {
		return errs.New(
			errs.M("couldn't get output parameters for task", t.Name(), t.Id()),
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

		if err := oa.UpdateSource(
			ctx, doo[index].ItemDefinition(), data.Recalculate,
		); err != nil {
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

// updateOutputs checks all Task's output parameters and if it's not in Ready
// state it tries to fill it from the Scope.
func (t *task) updateOutputs(ctx context.Context, s scope.Scope) ([]*data.Parameter, error) {
	oo, err := t.IoSpec.Parameters(data.Output)
	if err != nil {
		return nil, fmt.Errorf("couldn't get task's output parameters")
	}

	for _, o := range oo {
		if o.State().Name() == data.ReadyDataState.Name() {
			continue
		}

		d, err := s.GetDataById(t.dataPath, o.ItemDefinition().Id())
		if err != nil {
			return nil,
				fmt.Errorf("couldn't get data #%s from Scope: %w",
					o.ItemDefinition().Id(), err)
		}

		if d.State().Name() != data.ReadyDataState.Name() {
			return nil,
				fmt.Errorf("data isn't Ready for update task's output #%s",
					d.ItemDefinition().Id())
		}

		if err := o.Value().Update(ctx, d.Value().Get(ctx)); err != nil {
			return nil,
				fmt.Errorf("couldn't update task output #%s: %w",
					o.ItemDefinition().Id(), err)
		}

		if err := o.UpdateState(data.ReadyDataState); err != nil {
			return nil,
				fmt.Errorf("couldn't set task output #%s state to Ready: %w",
					o.ItemDefinition().Id(), err)
		}
	}

	return oo, nil
}

// --------------------- flow.AssociationSource --------------------------------

// Outputs returns a list of output parameters of the Task
func (t *task) Outputs() []*data.ItemAwareElement {
	return t.getParams(data.Output)
}

// BindOutgoing adds new outgoing data association.
func (t *task) BindOutgoing(oa *data.Association) error {
	return t.bindAssociation(oa, data.Output)
}

// getParams returns a list of the Task parameters input or output according to
// direction dir.
func (t *task) getParams(dir data.Direction) []*data.ItemAwareElement {
	pp := []*data.ItemAwareElement{}

	params, _ := t.IoSpec.Parameters(dir)
	for _, p := range params {
		pp = append(pp, &p.ItemAwareElement)
	}

	return pp
}

// bindAssociation binds data association to the Task according to dir either
// input or output.
func (t *task) bindAssociation(a *data.Association, dir data.Direction) error {
	if a == nil {
		return fmt.Errorf("couldn't bind empty association")
	}

	if slices.ContainsFunc(
		t.dataAssociations[dir],
		func(da *data.Association) bool {
			return da.Id() == a.Id()
		}) {
		return fmt.Errorf("association #%s already binded", a.Id())
	}

	// TODO: Consider checking existence of parameter equal to
	// a source or target.

	t.dataAssociations[dir] = append(
		t.dataAssociations[dir], a)

	return nil
}

// --------------------- flow.AssociationTarget --------------------------------

// Inputs returns list of input parameters's ItemAwareElements.
func (t *task) Inputs() []*data.ItemAwareElement {
	return t.getParams(data.Input)
}

// BindIncoming adds new incoming data association to the Task.
func (t *task) BindIncoming(ia *data.Association) error {
	return t.bindAssociation(ia, data.Input)
}

// -----------------------------------------------------------------------------

// interfaces check
var (
	_ flow.ActivityNode      = (*task)(nil)
	_ scope.NodeDataLoader   = (*task)(nil)
	_ scope.NodeDataConsumer = (*task)(nil)
	_ scope.NodeDataProducer = (*task)(nil)
	_ flow.AssociationSource = (*task)(nil)
	_ flow.AssociationTarget = (*task)(nil)
)
