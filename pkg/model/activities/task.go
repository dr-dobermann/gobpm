package activities

import (
	"context"
	"fmt"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"slices"

	"github.com/dr-dobermann/gobpm/pkg/datastore"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
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
			err := o(&mInst)
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
func (t *task) clone() (task, error) {
	a, err := t.activity.clone()
	if err != nil {
		return task{}, err
	}

	return task{
		activity:      a,
		multyInstance: t.multyInstance,
	}, nil
}

// IsMultyinstance returns Task multyinstance settings.
func (t *task) IsMultyinstance() bool {
	return t.multyInstance
}

// --------------------- flow.ActivityNode interface ---------------------------

func (t *task) ActivityType() flow.ActivityType {
	return flow.TaskActivity
}

// ------------------ exec.NodeDataConsumer interface --------------------------

// LoadData instantiates the Task's inputs, outputs and properties in the
// execution frame and fills the input instances from the Task's incoming
// data associations. The IoSpec definitions on the node stay untouched —
// every execution works on its own instances (ADR-010 §2.3).
func (t *task) LoadData(ctx context.Context, f exec.Frame) error {
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
				errs.D(observability.AttrNodeName, t.Name()))
		}

		// A DataStoreReference input reads the engine-global Data Store
		// (SRD-068 FR-4, DataStore → Node), not per-instance scope; an
		// unregistered store or a read failure is a hard error (fail-loud).
		if ref := ia.DataStoreRef(); ref != "" {
			if err := t.loadFromStore(
				ctx, f, ref, ia, dii[index], gating); err != nil {
				return err
			}

			continue
		}

		// Otherwise the association's source is THIS instance's DataObject
		// (SRD-063 FR-5, DataObject → Node), resolved from the frame's scope
		// by name.
		if err := t.loadFromScope(ctx, f, ia, dii[index], gating); err != nil {
			return err
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
func (t *task) instantiateData(f exec.Frame) error {
	inputs, err := t.IoSpec.Parameters(data.Input)
	if err != nil {
		return errs.New(
			errs.M("couldn't get task inputs"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D(observability.AttrNodeName, t.Name()),
			errs.E(err))
	}

	if err = f.InstantiateInputs(inputs); err != nil {
		return errs.New(
			errs.M("couldn't instantiate task inputs"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D(observability.AttrNodeName, t.Name()),
			errs.E(err))
	}

	outputs, err := t.IoSpec.Parameters(data.Output)
	if err != nil {
		return errs.New(
			errs.M("couldn't get task outputs"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D(observability.AttrNodeName, t.Name()),
			errs.E(err))
	}

	if err := f.InstantiateOutputs(outputs); err != nil {
		return errs.New(
			errs.M("couldn't instantiate task outputs"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D(observability.AttrNodeName, t.Name()),
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
			errs.D(observability.AttrNodeName, t.Name()),
			errs.E(err))
	}

	return nil
}

// ------------------ exec.NodeDataProducer interface --------------------------

// UploadData fills the not-Ready output instances of the execution frame and
// pushes the Task's outgoing data associations from those instances.
func (t *task) UploadData(ctx context.Context, f exec.Frame) error {
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

		// an optional output that wasn't produced has no value to push.
		if doo[index].State().Name() != data.ReadyDataState.Name() {
			continue
		}

		// A DataStoreReference output writes the engine-global Data Store
		// (SRD-068 FR-4, Node → DataStore), not per-instance scope; an
		// unregistered store or a write failure is a hard error (fail-loud).
		if ref := oa.DataStoreRef(); ref != "" {
			if err := t.uploadToStore(ctx, f, ref, oa, doo[index]); err != nil {
				return err
			}

			continue
		}

		// Write the produced value into THIS instance's DataObject (the
		// association's target), resolved from the frame's scope (SRD-063 FR-5,
		// output direction: Node → DataObject). The DataAssociation is shared
		// across instances, so we never mutate it — we read its routing (target
		// id) and update the per-instance DataObject in place (scope holds it by
		// reference, so the write is visible to by-name reads). (Transformation/
		// assignment is a noted follow-up — SRD-063 §10.3.)
		doDatum, gerr := f.GetData(oa.TargetName())
		if gerr != nil {
			return errs.New(
				errs.M("couldn't resolve DataObject %q for task %q[%s] "+
					"output association %q", oa.TargetName(),
					t.Name(), t.ID(), oa.ID()),
				errs.C(errorClass, errs.ObjectNotFound),
				errs.E(gerr))
		}

		if uerr := doDatum.Value().Update(
			ctx, doo[index].Value().Get(ctx)); uerr != nil {
			return errs.New(
				errs.M("couldn't update DataObject %q value for task %q[%s]",
					oa.TargetName(), t.Name(), t.ID()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(uerr))
		}

		// SRD-063: the produced value was written into a per-instance Data
		// Object (observability). The in-place update bypasses the frame
		// commit-diff, so this is its only fact.
		f.RecordDataMovement(false, true, oa.TargetName(), "")
	}

	return nil
}

// loadFromScope fills the task input dst from THIS instance's DataObject (the
// association's source) resolved from the frame's scope by name (SRD-063 FR-5,
// DataObject → Node). The DataAssociation is a shared declaration — we read its
// routing (source id), the value comes from the per-instance DataObject. A
// required input that can't be filled fails fast; gobpm never waits for data
// (ADR-011 v.2 §2.3). (A transformation/assignment on a DataObject association
// is a noted follow-up — SRD-063 §10.3; the current uses are plain copies.)
func (t *task) loadFromScope(
	ctx context.Context,
	f exec.Frame,
	ia *data.Association,
	dst *data.Parameter,
	gating map[string]bool,
) error {
	src := ia.SourceNames()

	var (
		doDatum data.Data
		derr    error
	)

	if len(src) > 0 {
		doDatum, derr = f.GetData(src[0])
	}

	if len(src) == 0 || derr != nil ||
		doDatum.State().Name() != data.ReadyDataState.Name() {
		if gating[ia.TargetItemDefID()] {
			return errs.New(
				errs.M("required input %q of task %q is unavailable "+
					"(gobpm does not wait for data)",
					dst.Name(), t.Name()),
				errs.C(errorClass, errs.ConditionFailed))
		}

		return nil
	}

	if err := dst.Subject().Structure().
		Update(ctx, doDatum.Value().Get(ctx)); err != nil {
		return t.opErr("couldn't update input "+dst.Name(), err)
	}

	// a DataInput filled by its DataInputAssociation becomes available
	// (BPMN §10.4.2) — the state flip targets the frame instance only.
	if err := dst.UpdateState(data.ReadyDataState); err != nil {
		return t.opErr("couldn't set input "+dst.Name()+" to Ready", err)
	}

	// SRD-063: a per-instance Data Object was read into the node (observability).
	f.RecordDataMovement(false, false, src[0], "")

	return nil
}

// opErr wraps err as an OperationFailed task error with msg. It is a single-line
// builder so a defensive propagation return (a fill/clone that cannot fail in
// practice — the frame input's structure is permissive) rides the project's
// error-return exclude convention rather than a fake-coverage test.
func (t *task) opErr(msg string, err error) error {
	return errs.New(errs.M("%s", msg), errs.C(errorClass, errs.OperationFailed), errs.E(err))
}

// storeFor resolves the engine Data Store named ref from the frame's registry
// (SRD-068 FR-4). It fails loud: no registry wired, or an unregistered ref, is
// a configuration error — a DataStoreReference must name a registered store.
func (t *task) storeFor(f exec.Frame, ref string) (datastore.DataStore, error) {
	reg := f.DataStores()
	if reg == nil {
		return nil, errs.New(
			errs.M("no Data Store registry wired for task %q[%s]", t.Name(), t.ID()),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	store, err := reg.Store(ref)
	if err != nil {
		return nil, errs.New(
			errs.M("couldn't resolve DataStore %q for task %q[%s]",
				ref, t.Name(), t.ID()),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.E(err))
	}

	return store, nil
}

// loadFromStore fills the task input dst from the engine Data Store named ref
// (SRD-068 FR-4, DataStore → Node): the value is read by the association's item
// name. An absent value is fail-fast only when the input gates the start
// (ADR-011 v.2 §2.3); an unregistered store or a read error is always a hard
// error.
func (t *task) loadFromStore(
	ctx context.Context,
	f exec.Frame,
	ref string,
	ia *data.Association,
	dst *data.Parameter,
	gating map[string]bool,
) error {
	store, err := t.storeFor(f, ref)
	if err != nil {
		return err
	}

	var d data.Data

	if src := ia.SourceNames(); len(src) > 0 {
		var ok bool
		if d, ok, err = store.Get(ctx, src[0]); err != nil {
			return errs.New(
				errs.M("couldn't read DataStore %q key %q for task %q",
					ref, src[0], t.Name()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		} else if !ok {
			d = nil
		}
	}

	if d == nil || d.State().Name() != data.ReadyDataState.Name() {
		if gating[ia.TargetItemDefID()] {
			return errs.New(
				errs.M("required input %q of task %q is unavailable in "+
					"DataStore %q (gobpm does not wait for data)",
					dst.Name(), t.Name(), ref),
				errs.C(errorClass, errs.ConditionFailed))
		}

		return nil
	}

	if err := dst.Subject().Structure().
		Update(ctx, d.Value().Get(ctx)); err != nil {
		return t.opErr("couldn't update input "+dst.Name()+" from DataStore "+ref, err)
	}

	if err := dst.UpdateState(data.ReadyDataState); err != nil {
		return t.opErr("couldn't set input "+dst.Name()+" to Ready", err)
	}

	// SRD-068: a value was read from the engine Data Store into the node
	// (observability). A Ready d implies the association named a store key.
	f.RecordDataMovement(true, false, ia.SourceNames()[0], ref)

	return nil
}

// uploadToStore writes the produced output src into the engine Data Store named
// ref (SRD-068 FR-4, Node → DataStore), keyed by the association's item name. A
// clone is stored so the store's datum is independent of the per-execution
// output instance.
func (t *task) uploadToStore(
	ctx context.Context,
	f exec.Frame,
	ref string,
	oa *data.Association,
	src *data.Parameter,
) error {
	store, err := t.storeFor(f, ref)
	if err != nil {
		return err
	}

	datum, cerr := src.Clone()
	if cerr != nil {
		return t.opErr("couldn't clone output "+src.Name()+" for DataStore "+ref, cerr)
	}

	if perr := store.Put(ctx, oa.TargetName(), datum); perr != nil {
		return errs.New(
			errs.M("couldn't write output %q into DataStore %q key %q for "+
				"task %q", src.Name(), ref, oa.TargetName(), t.Name()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(perr))
	}

	// SRD-068: the produced value was written into the engine Data Store
	// (observability), keyed by the association's target name.
	f.RecordDataMovement(true, true, oa.TargetName(), ref)

	return nil
}

// updateOutputs checks the frame's output instances and fills every not-Ready
// one from the frame's resolution (puts, inputs, container walk). It is the
// completion-gate (ADR-011 v.2 §2.2): a required output that cannot be produced
// is an error — gobpm never silently produces nothing — while an optional output
// may be left Unavailable.
func (t *task) updateOutputs(
	ctx context.Context,
	f exec.Frame,
) ([]*data.Parameter, error) {
	oo := f.Outputs()
	required := data.RequiredItemIDs(t.IoSpec.OutputSet())

	for _, o := range oo {
		if o.State().Name() == data.ReadyDataState.Name() {
			continue
		}

		id := o.ItemDefinition().ID()

		d, err := f.GetDataByID(id)
		if err != nil || d.State().Name() != data.ReadyDataState.Name() {
			// the output wasn't produced: a required output is an error, an
			// optional one may be absent.
			if required[id] {
				return nil,
					fmt.Errorf("required output #%s of task %q was not produced",
						id, t.Name())
			}

			continue
		}

		if err := o.Value().Update(ctx, d.Value().Get(ctx)); err != nil {
			return nil,
				fmt.Errorf("couldn't update task output #%s: %w", id, err)
		}

		if err := o.UpdateState(data.ReadyDataState); err != nil {
			return nil,
				fmt.Errorf("couldn't set task output #%s state to Ready: %w",
					id, err)
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
	params, err := t.IoSpec.Parameters(dir)
	if err != nil {
		// getParams is called only with the data.Input/data.Output constants
		// (Outputs/Inputs) — Parameters cannot fail here unless the Direction
		// constants are broken; fail loudly (FIX-028) instead of reporting a
		// parameterless task.
		errs.Panic(err)
	}
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
	_ exec.NodeDataConsumer  = (*task)(nil)
	_ exec.NodeDataProducer  = (*task)(nil)
	_ flow.AssociationSource = (*task)(nil)
	_ flow.AssociationTarget = (*task)(nil)
)
