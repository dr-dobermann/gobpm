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
			multyInstance: bool(mInst),
		},
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

// ------------------ scope.NodeDataConsumer interface --------------------------

// LoadData loads data from Task's incoming data associations into its
// inputs.
func (t *Task) LoadData(ctx context.Context) error {
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

		if err := dii[index].Subject().Structure().Update(v); err != nil {
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

// ------------------ scope.NodeDataProducer interface --------------------------

// UploadData fills all Task's outputs with not-Ready state from the Scope and
// loads all Task's outgoing data associations from Task's outputs.
func (t *Task) UploadData(ctx context.Context, s scope.Scope) error {
	doo, err := t.updateOutputs(s)
	if err != nil {
		return errs.New(
			errs.M("couldn't tt output parameters for task", t.Name(), t.Id()),
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

		if err := oa.UpdateSource(ctx, doo[index].ItemDefinition()); err != nil {
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
func (t *Task) updateOutputs(s scope.Scope) ([]*data.Parameter, error) {
	oo, err := t.IoSpec.Parameters(data.Output)
	if err != nil {
		return nil, errs.New(
			errs.M("couldn't get task's output parameters"),
			errs.E(err))
	}

	for _, o := range oo {
		if o.State().Name() == data.ReadyDataState.Name() {
			continue
		}

		d, err := s.GetDataById(t.dataPath, o.ItemDefinition().Id())
		if err != nil {
			return nil,
				errs.New(
					errs.M("couldn't get data from Scope"),
					errs.E(err),
					errs.D("item_definitio_id", o.ItemDefinition().Id()))
		}

		if d.State().Name() != data.ReadyDataState.Name() {
			return nil,
				errs.New(
					errs.M("data isn't Ready for update task's output"),
					errs.D("data_name", d.Name()),
					errs.D("item_definition_id", o.ItemDefinition().Id()),
					errs.D("output_name", o.Name()))
		}

		if err := o.Value().Update(d.Value().Get()); err != nil {
			return nil,
				errs.New(
					errs.M("couldn't update task output"),
					errs.E(err),
					errs.D("output_name", o.Name()),
					errs.D("data_name", d.Name()),
					errs.D("item_definition_id", o.ItemDefinition().Id()))
		}

		if err := o.UpdateState(data.ReadyDataState); err != nil {
			return nil,
				errs.New(
					errs.M("couldn't set task output state to Ready"),
					errs.E(err),
					errs.D("output_name", o.Name()))
		}
	}

	return oo, nil
}

// --------------------- flow.AssociationSource --------------------------------

// Outputs returns a list of output parameters of the Task
func (t *Task) Outputs() []*data.ItemAwareElement {
	outputs := []*data.ItemAwareElement{}

	opp, _ := t.IoSpec.Parameters(data.Output)
	for _, op := range opp {
		outputs = append(outputs, &op.ItemAwareElement)
	}

	return outputs
}

// BindOutgoing adds new outgoing data association.
func (t *Task) BindOutgoing(oa *data.Association) error {
	if oa == nil {
		return errs.New(
			errs.M("couldn't bind empty association"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if slices.ContainsFunc(
		t.dataAssociations[data.Output],
		func(a *data.Association) bool {
			return a.Id() == oa.Id()
		}) {
		return errs.New(
			errs.M("association already binded"),
			errs.C(errorClass, errs.DuplicateObject),
			errs.D("association_id", oa.Id()))
	}

	// TODO: Consider checking existence of output parameter equal to
	// oa source.

	t.dataAssociations[data.Output] = append(
		t.dataAssociations[data.Output], oa)

	return nil
}

// --------------------- flow.AssociationTarget --------------------------------

// Inputs returns list of input parameters's ItemAwareElements.
func (t *Task) Inputs() []*data.ItemAwareElement {
	inputs := []*data.ItemAwareElement{}

	ipp, _ := t.IoSpec.Parameters(data.Input)
	for _, ip := range ipp {
		inputs = append(inputs, &ip.ItemAwareElement)
	}

	return inputs
}

// BindIncoming adds new incoming data association to the Task.
func (t *Task) BindIncoming(ia *data.Association) error {
	if ia == nil {
		return errs.New(
			errs.M("couldn't bind empty association"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if slices.ContainsFunc(
		t.dataAssociations[data.Input],
		func(a *data.Association) bool {
			return a.Id() == ia.Id()
		}) {
		return errs.New(
			errs.M("association already binded"),
			errs.C(errorClass, errs.DuplicateObject),
			errs.D("association_id", ia.Id()))
	}

	// TODO: Consider checking existence of input parameter equal to
	// oa source.

	t.dataAssociations[data.Input] = append(
		t.dataAssociations[data.Input], ia)

	return nil
}

// -----------------------------------------------------------------------------

// interfaces check
var (
	_ flow.ActivityNode      = (*Task)(nil)
	_ scope.NodeDataLoader   = (*Task)(nil)
	_ scope.NodeDataConsumer = (*Task)(nil)
	_ scope.NodeDataProducer = (*Task)(nil)
	_ flow.AssociationSource = (*Task)(nil)
	_ flow.AssociationTarget = (*Task)(nil)
)
