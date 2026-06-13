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

// clone returns a per-instance copy of the task: the embedded activity is cloned
// (config shared by reference, fresh shell, zero dataPath) and the
// multyInstance flag is copied as configuration.
func (t *task) clone() task {
	return task{
		activity:      t.activity.clone(),
		multyInstance: t.multyInstance,
	}
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

// LoadData instantiates the Task's inputs, outputs and properties in the
// execution frame and fills the input instances from the Task's incoming
// data associations. The IoSpec definitions on the node stay untouched —
// every execution works on its own instances (ADR-010 §2.3).
func (t *task) LoadData(ctx context.Context, f *scope.Frame) error {
	if err := t.instantiateData(f); err != nil {
		return err
	}

	// an input gates the activity's start unless it is optional or
	// while-executing (ADR-011 v.2 §2.2). InputSet is the input parameter list.
	gating := data.RequiredItemIDs(t.IoSpec.InputSet())

	dii := f.Inputs()

	for _, ia := range t.dataAssociations[data.Input] {
		index := slices.IndexFunc(
			dii,
			func(i *data.Parameter) bool {
				return ia.TargetItemDefID() == i.ItemDefinition().ID()
			})
		if index == -1 {
			return errs.New(
				errs.M("couldn't find task input for association's %q target %q",
					ia.ID(), ia.TargetItemDefID()),
				errs.C(errorClass),
				errs.D("task_name", t.Name()))
		}

		v, err := ia.Value(ctx)
		if err != nil {
			// a required input that can't be filled is a fail-fast error —
			// gobpm never waits for data (ADR-011 v.2 §2.3). An optional or
			// while-executing input may stay Unavailable.
			if gating[ia.TargetItemDefID()] {
				return errs.New(
					errs.M("required input %q of task %q is unavailable "+
						"(gobpm does not wait for data)",
						dii[index].Name(), t.Name()),
					errs.C(errorClass, errs.ConditionFailed),
					errs.E(err))
			}

			continue
		}

		if err := dii[index].Subject().Structure().Update(ctx, v.Structure().Get(ctx)); err != nil {
			return errs.New(
				errs.M("couldn't update input %q", dii[index].Name()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}

		// a DataInput filled by its DataInputAssociation becomes available
		// (BPMN §10.4.2) — the state flip targets the frame instance only.
		if err := dii[index].UpdateState(data.ReadyDataState); err != nil {
			return errs.New(
				errs.M("couldn't set input %q to Ready", dii[index].Name()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}
	}

	// the start-gate: every required input must now be available — this also
	// catches a required input with no association to fill it.
	for _, in := range dii {
		if !gating[in.ItemDefinition().ID()] {
			continue
		}

		if in.State().Name() != data.ReadyDataState.Name() {
			return errs.New(
				errs.M("required input %q of task %q is unavailable "+
					"(gobpm does not wait for data)",
					in.Name(), t.Name()),
				errs.C(errorClass, errs.ConditionFailed))
		}
	}

	return nil
}

// instantiateData builds the per-execution instances of the Task's data
// definitions in the frame: inputs, outputs, and properties.
func (t *task) instantiateData(f *scope.Frame) error {
	inputs, err := t.IoSpec.Parameters(data.Input)
	if err != nil {
		return errs.New(
			errs.M("couldn't get task inputs"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("task_name", t.Name()),
			errs.E(err))
	}

	if err = f.InstantiateInputs(inputs); err != nil {
		return errs.New(
			errs.M("couldn't instantiate task inputs"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("task_name", t.Name()),
			errs.E(err))
	}

	outputs, err := t.IoSpec.Parameters(data.Output)
	if err != nil {
		return errs.New(
			errs.M("couldn't get task outputs"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("task_name", t.Name()),
			errs.E(err))
	}

	if err := f.InstantiateOutputs(outputs); err != nil {
		return errs.New(
			errs.M("couldn't instantiate task outputs"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("task_name", t.Name()),
			errs.E(err))
	}

	props := make([]*data.Property, 0, len(t.properties))
	for _, p := range t.properties {
		props = append(props, p)
	}

	if err := f.LoadProperties(props); err != nil {
		return errs.New(
			errs.M("couldn't load task properties"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("task_name", t.Name()),
			errs.E(err))
	}

	return nil
}

// ------------------ scope.NodeDataProducer interface --------------------------

// UploadData fills the not-Ready output instances of the execution frame and
// pushes the Task's outgoing data associations from those instances.
func (t *task) UploadData(ctx context.Context, f *scope.Frame) error {
	doo, err := t.updateOutputs(ctx, f)
	if err != nil {
		return errs.New(
			errs.M("couldn't get output parameters for task", t.Name(), t.ID()),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.E(err))
	}

	for _, oa := range t.dataAssociations[data.Output] {
		index := slices.IndexFunc(doo,
			func(o *data.Parameter) bool {
				return oa.HasSourceID(o.Subject().ID())
			})

		if index == -1 {
			return errs.New(
				errs.M("couldn't find task's %q[%s] output for association %q",
					t.Name(), t.ID(), oa.ID()),
				errs.C(errorClass, errs.ObjectNotFound))
		}

		if err := oa.UpdateSource(
			ctx,
			doo[index].ItemDefinition(),
			data.Recalculate,
		); err != nil {
			return errs.New(
				errs.M("couldn't update association's %q source %q for "+
					"task %q[%s]", oa.ID(), doo[index].ItemDefinition().ID(),
					t.Name(), t.ID()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}
	}

	return nil
}

// updateOutputs checks the frame's output instances and fills every
// not-Ready one from the frame's resolution (puts, inputs, container walk).
func (t *task) updateOutputs(
	ctx context.Context,
	f *scope.Frame,
) ([]*data.Parameter, error) {
	oo := f.Outputs()

	for _, o := range oo {
		if o.State().Name() == data.ReadyDataState.Name() {
			continue
		}

		d, err := f.GetDataByID(o.ItemDefinition().ID())
		if err != nil {
			return nil,
				fmt.Errorf("couldn't get data #%s from the frame: %w",
					o.ItemDefinition().ID(), err)
		}

		if d.State().Name() != data.ReadyDataState.Name() {
			return nil,
				fmt.Errorf("data isn't Ready for update task's output #%s",
					d.ItemDefinition().ID())
		}

		if err := o.Value().Update(ctx, d.Value().Get(ctx)); err != nil {
			return nil,
				fmt.Errorf("couldn't update task output #%s: %w",
					o.ItemDefinition().ID(), err)
		}

		if err := o.UpdateState(data.ReadyDataState); err != nil {
			return nil,
				fmt.Errorf("couldn't set task output #%s state to Ready: %w",
					o.ItemDefinition().ID(), err)
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
	params, _ := t.IoSpec.Parameters(dir)
	pp := make([]*data.ItemAwareElement, 0, len(params))
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
			return da.ID() == a.ID()
		}) {
		return fmt.Errorf("association #%s already binded", a.ID())
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
	_ scope.NodeDataConsumer = (*task)(nil)
	_ scope.NodeDataProducer = (*task)(nil)
	_ flow.AssociationSource = (*task)(nil)
	_ flow.AssociationTarget = (*task)(nil)
)
