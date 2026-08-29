// Package dataflow is the one copy path a data association runs on at
// execution time — for a task and for an event alike (SRD-094 FR-3). An
// association is routing, read for its names; the values move between the
// frame's per-execution parameter instances and the per-instance data the
// frame resolves by name (SRD-063 FR-5) or the engine Data Store the
// association names (SRD-068 FR-4). No model object is mutated at run time.
package dataflow

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/datastore"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

const errorClass = "DATAFLOW_ERRORS"

// FillInput fills the frame input dst from its association's source: the
// per-instance datum resolved by name from the frame (SRD-063 FR-5,
// DataObject → Node) or, when the association names a Data Store, that
// store's value under the source name (SRD-068 FR-4). A filled input flips
// Ready — a DataInput filled by its association becomes available (BPMN
// §10.4.2). A source that is absent or not Ready fills nothing: an input
// that gates the owner's start (gating[item id]) then fails fast — gobpm
// never waits for data (ADR-011 §2.3) — and an optional one stays
// Unavailable. owner labels the errors ("task \"x\"[id]").
func FillInput(
	ctx context.Context,
	f exec.Frame,
	ia *data.Association,
	dst *data.Parameter,
	gating map[string]bool,
	owner string,
) error {
	// An association carrying an expression shape (ADR-011 §2.4) is filled by
	// evaluating it, not by copying a source: its sources gate the fill, the
	// expression produces the value, and neither half reads a source datum.
	if shaped(ia) {
		return fillByShape(ctx, f, ia, dst, gating, owner)
	}

	if ref := ia.DataStoreRef(); ref != "" {
		return fillFromStore(ctx, f, ref, ia, dst, gating, owner)
	}

	return fillFromScope(ctx, f, ia, dst, gating, owner)
}

// PushOutput copies the Ready output instance src into its association's
// target: the per-instance datum resolved by name from the frame (SRD-063
// FR-5, Node → DataObject), updated in place, or the Data Store the
// association names (SRD-068 FR-4). A src that is not Ready — an optional
// output the owner did not produce — pushes nothing (ADR-011 §2.5: it simply
// does not flow). Each movement is recorded for observability.
func PushOutput(
	ctx context.Context,
	f exec.Frame,
	oa *data.Association,
	src *data.Parameter,
	owner string,
) error {
	if src.State().Name() != data.ReadyDataState.Name() {
		return nil
	}

	if shaped(oa) {
		return pushByShape(ctx, f, oa, src, owner)
	}

	if ref := oa.DataStoreRef(); ref != "" {
		return pushToStore(ctx, f, ref, oa, src, owner)
	}

	return pushToScope(ctx, f, oa, src, owner)
}

// fillFromScope is FillInput's scope half.
func fillFromScope(
	ctx context.Context,
	f exec.Frame,
	ia *data.Association,
	dst *data.Parameter,
	gating map[string]bool,
	owner string,
) error {
	src := ia.SourceNames()

	var (
		datum data.Data
		derr  error
	)

	if len(src) > 0 {
		datum, derr = f.GetData(src[0])
	}

	if len(src) == 0 || derr != nil ||
		datum.State().Name() != data.ReadyDataState.Name() {
		if gating[ia.TargetItemDefID()] {
			return errs.New(
				errs.M("required input %q of %s is unavailable "+
					"(gobpm does not wait for data)", dst.Name(), owner),
				errs.C(errorClass, errs.ConditionFailed))
		}

		return nil
	}

	if err := fill(ctx, dst, datum); err != nil {
		return opErr("couldn't update input "+dst.Name()+" of "+owner, err)
	}

	// SRD-063: a per-instance Data Object was read into the node.
	f.RecordDataMovement(false, false, src[0], "")

	return nil
}

// fillByShape is FillInput's expression half (ADR-011 §2.4 rules 1 and 2):
// the association's sources gate the fill and its expression produces the
// value, which lands in the frame's input instance exactly as a copied one
// does — the parameter is then Ready and gates the activity's start like any
// other filled input.
//
// The gate differs from the plain copy's in one way the standard requires:
// EVERY source must be available, not just the one a copy would read
// (§10.4.2 — "if any of the sources is in the state of unavailable, the data
// association cannot be executed"), because a transformation may read all of
// them or none.
func fillByShape(
	ctx context.Context,
	f exec.Frame,
	ia *data.Association,
	dst *data.Parameter,
	gating map[string]bool,
	owner string,
) error {
	if why := unreadySource(f, ia); why != "" {
		if gating[ia.TargetItemDefID()] {
			return errs.New(
				errs.M("required input %q of %s is unavailable: association "+
					"%q has no value to evaluate — %s (gobpm does not wait "+
					"for data)", dst.Name(), owner, ia.ID(), why),
				errs.C(errorClass, errs.ConditionFailed))
		}

		return nil
	}

	if err := applyShape(
		ctx, f, ia, dst.Subject().Structure(), dst.Name(), owner,
	); err != nil {
		return err
	}

	// The frame instance's structure is permissive and Ready is a registered
	// state; said in the form the coverage gate reads.
	if err := dst.UpdateState(data.ReadyDataState); err != nil {
		return opErr("couldn't mark input "+dst.Name()+" of "+owner+" Ready", err)
	}

	// One fact per source, each labeled by what it actually is: a
	// store-backed association reads the engine store, any other reads a
	// per-instance Data Object, and recording the association's store ref
	// against a Data Object would name a store nothing was read from.
	store := ia.DataStoreRef()
	for _, name := range ia.SourceNames() {
		f.RecordDataMovement(store != "", false, name, store)
	}

	return nil
}

// pushByShape is PushOutput's expression half: the association's expression
// produces what lands in the target, whether that target is a per-instance
// Data Object or an engine Data Store key.
func pushByShape(
	ctx context.Context,
	f exec.Frame,
	oa *data.Association,
	src *data.Parameter,
	owner string,
) error {
	if ref := oa.DataStoreRef(); ref != "" {
		return pushShapedToStore(ctx, f, ref, oa, src, owner)
	}

	datum, err := f.GetData(oa.TargetName())
	if err != nil {
		return errs.New(
			errs.M("couldn't resolve DataObject %q for %s output "+
				"association %q", oa.TargetName(), owner, oa.ID()),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.E(err))
	}

	if err := applyShape(
		ctx, f, oa, datum.Value(), oa.TargetName(), owner,
	); err != nil {
		return err
	}

	// An ItemAwareElement refuses no state it knows; said in the form the
	// coverage gate reads.
	if st, ok := datum.(stateful); ok {
		if err := st.UpdateState(data.ReadyDataState); err != nil {
			return opErr("couldn't mark DataObject "+oa.TargetName()+" Ready for "+owner, err)
		}
	}

	f.RecordDataMovement(false, true, oa.TargetName(), "")

	return nil
}

// pushShapedToStore is pushByShape's Data Store half: the expression writes
// into a clone of the output instance, which is then stored under the
// association's target name — the store's datum stays independent of the
// per-execution instance, exactly as a plain push keeps it.
func pushShapedToStore(
	ctx context.Context,
	f exec.Frame,
	ref string,
	oa *data.Association,
	src *data.Parameter,
	owner string,
) error {
	store, err := storeFor(f, ref, owner)
	if err != nil {
		return err
	}

	// WHAT the shape writes into differs by shape, and the difference is
	// the standard's own (§10.4.2). A transformation REPLACES the target,
	// so a clone of the output instance is the right canvas — nothing of
	// the store's previous value survives a replace. Assignments write
	// INSIDE the target, so they need the store's CURRENT value: writing
	// them into a clone of the source would both address a different shape
	// and drop every field the assignments do not touch, because Put
	// replaces the whole key.
	datum, err := storeTarget(ctx, store, oa, src, ref, owner)
	if err != nil {
		return err
	}

	if err := applyShape(
		ctx, f, oa, datum.Value(), oa.TargetName(), owner,
	); err != nil {
		return err
	}

	if err := store.Put(ctx, oa.TargetName(), datum); err != nil {
		return errs.New(
			errs.M("couldn't write output %q into DataStore %q key %q for %s",
				src.Name(), ref, oa.TargetName(), owner),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	f.RecordDataMovement(true, true, oa.TargetName(), ref)

	return nil
}

// storeTarget is the value an output shape writes into: the store's
// current value under the association's key when the shape writes inside
// it, a clone of the produced output when the shape replaces it whole.
//
// A missing key is not an error for either: an assignment into a key the
// store does not hold yet writes into the produced output's clone, which
// is the only shape available — the alternative would be refusing the
// first write to a store.
func storeTarget(
	ctx context.Context,
	store datastore.DataStore,
	oa *data.Association,
	src *data.Parameter,
	ref, owner string,
) (data.Data, error) {
	if len(oa.Assignments()) != 0 {
		current, ok, err := store.Get(ctx, oa.TargetName())
		if err != nil {
			return nil, errs.New(
				errs.M("couldn't read DataStore %q key %q for %s before "+
					"writing its assignments", ref, oa.TargetName(), owner),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}

		if ok {
			return current, nil
		}
	}

	// A Ready output instance always clones; said in the form the coverage
	// gate reads.
	datum, err := src.Clone()
	if err != nil {
		return nil, opErr("couldn't clone output "+src.Name()+" of "+owner+" for DataStore "+ref, err)
	}

	return datum, nil
}

// fillFromStore is FillInput's Data Store half: the value is read by the
// association's source name; an unregistered store or a read error is
// always a hard error.
func fillFromStore(
	ctx context.Context,
	f exec.Frame,
	ref string,
	ia *data.Association,
	dst *data.Parameter,
	gating map[string]bool,
	owner string,
) error {
	store, err := storeFor(f, ref, owner)
	if err != nil {
		return err
	}

	var d data.Data

	if src := ia.SourceNames(); len(src) > 0 {
		var ok bool
		if d, ok, err = store.Get(ctx, src[0]); err != nil {
			return errs.New(
				errs.M("couldn't read DataStore %q key %q for %s",
					ref, src[0], owner),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		} else if !ok {
			d = nil
		}
	}

	if d == nil || d.State().Name() != data.ReadyDataState.Name() {
		if gating[ia.TargetItemDefID()] {
			return errs.New(
				errs.M("required input %q of %s is unavailable in "+
					"DataStore %q (gobpm does not wait for data)",
					dst.Name(), owner, ref),
				errs.C(errorClass, errs.ConditionFailed))
		}

		return nil
	}

	if err := fill(ctx, dst, d); err != nil {
		return opErr("couldn't update input "+dst.Name()+" of "+owner+" from DataStore "+ref, err)
	}

	// SRD-068: a value was read from the engine Data Store into the node. A
	// Ready d implies the association named a store key.
	f.RecordDataMovement(true, false, ia.SourceNames()[0], ref)

	return nil
}

// fill copies datum's value into the frame instance dst and flips it Ready
// — the state flip targets the frame instance only. Neither step fails in
// practice (the frame instance's structure is permissive, Ready is a
// registered state); the callers' error returns are said in the form the
// coverage gate reads.
func fill(ctx context.Context, dst *data.Parameter, datum data.Data) error {
	if err := dst.Subject().Structure().
		Update(ctx, datum.Value().Get(ctx)); err != nil {
		return err
	}

	return dst.UpdateState(data.ReadyDataState)
}

// stateful is a datum that can be marked Ready once it is produced — an
// ItemAwareElement, which a Data Object and a Property both are.
type stateful interface {
	UpdateState(*data.SrcState) error
}

// pushToScope is PushOutput's scope half: the association is read for its
// target name, and THIS instance's datum of that name is updated in place —
// scope holds it by reference, so the write is visible to by-name reads —
// and marked Ready: a Data Object fed by an association is Unavailable
// until its producer writes it (data.NewAssociation marks its target so),
// and only a Ready datum fills an input association downstream, so the
// flip is what makes the object readable through one.
// (A transformation/assignment is a noted follow-up — SRD-063 §10.3.)
func pushToScope(
	ctx context.Context,
	f exec.Frame,
	oa *data.Association,
	src *data.Parameter,
	owner string,
) error {
	datum, err := f.GetData(oa.TargetName())
	if err != nil {
		return errs.New(
			errs.M("couldn't resolve DataObject %q for %s output "+
				"association %q", oa.TargetName(), owner, oa.ID()),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.E(err))
	}

	// A committed datum's value is permissive on update; the branch is said
	// in the form the coverage gate reads.
	if err := datum.Value().Update(ctx, src.Value().Get(ctx)); err != nil {
		return opErr("couldn't update DataObject "+oa.TargetName()+" value for "+owner, err)
	}

	// An ItemAwareElement refuses no state it knows; said in the form the
	// coverage gate reads.
	if st, ok := datum.(stateful); ok {
		if err := st.UpdateState(data.ReadyDataState); err != nil {
			return opErr("couldn't mark DataObject "+oa.TargetName()+" Ready for "+owner, err)
		}
	}

	// SRD-063: the produced value was written into a per-instance Data
	// Object. The in-place update bypasses the frame commit-diff, so this is
	// its only fact.
	f.RecordDataMovement(false, true, oa.TargetName(), "")

	return nil
}

// pushToStore is PushOutput's Data Store half: a clone is stored under the
// association's target name so the store's datum is independent of the
// per-execution output instance.
func pushToStore(
	ctx context.Context,
	f exec.Frame,
	ref string,
	oa *data.Association,
	src *data.Parameter,
	owner string,
) error {
	store, err := storeFor(f, ref, owner)
	if err != nil {
		return err
	}

	// A Ready output instance always clones; said in the form the coverage
	// gate reads.
	datum, err := src.Clone()
	if err != nil {
		return opErr("couldn't clone output "+src.Name()+" of "+owner+" for DataStore "+ref, err)
	}

	if err := store.Put(ctx, oa.TargetName(), datum); err != nil {
		return errs.New(
			errs.M("couldn't write output %q into DataStore %q key %q for %s",
				src.Name(), ref, oa.TargetName(), owner),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	// SRD-068: the produced value was written into the engine Data Store,
	// keyed by the association's target name.
	f.RecordDataMovement(true, true, oa.TargetName(), ref)

	return nil
}

// storeFor resolves the engine Data Store named ref from the frame's
// registry (SRD-068 FR-4). It fails loud: no registry wired, or an
// unregistered ref, is a configuration error.
func storeFor(f exec.Frame, ref, owner string) (datastore.DataStore, error) {
	reg := f.DataStores()
	if reg == nil {
		return nil, errs.New(
			errs.M("no Data Store registry wired for %s", owner),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	store, err := reg.Store(ref)
	if err != nil {
		return nil, errs.New(
			errs.M("couldn't resolve DataStore %q for %s", ref, owner),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.E(err))
	}

	return store, nil
}

// opErr wraps err as an OperationFailed error with msg — a one-line builder
// for the defensive propagation returns (a fill or a clone that cannot fail
// in practice: the frame instance's structure is permissive).
func opErr(msg string, err error) error {
	return errs.New(errs.M("%s", msg), errs.C(errorClass, errs.OperationFailed), errs.E(err))
}
