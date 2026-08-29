package dataflow

import (
	"context"
	"fmt"
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// The association's two expression shapes (BPMN §10.4.2 rules 1 and 2,
// ADR-011 §2.4). A transformation's result REPLACES the target; each
// assignment writes its own result at its own path inside the target. The
// third shape — a plain single-source copy — needs none of this and takes
// the untouched branch in dataflow.go.

// frameSource adapts the execution frame to data.Source, which is what an
// expression evaluates against (ADR-011 §2.4): names resolve through the
// frame, so an expression reads the association's sources, the node's own
// data and structural paths into any of them, by the one resolver every
// other consumer reads through (§2.9.2).
type frameSource struct {
	f exec.Frame
}

// Find resolves name against the activity's data context: the scope the
// frame reads (data objects, properties, puts, provider sources) AND the
// node's own parameters.
//
// The parameters matter for the output direction and are why this is not
// simply GetData. An output association's expression exists to shape what
// the node JUST PRODUCED — its output parameter — and a frame resolves
// scope data, properties and inputs by name, never outputs: at that point
// in the node's life they are frame instances, not committed data. An
// expression that cannot read them could only reshape data the node did
// not produce, which is not what an output association is for.
//
// The lookup walks the same path resolver everything else does (ADR-011
// §2.9.2), so "note.code" reaches into an output exactly as
// "order.total" reaches into a data object.
func (s frameSource) Find(ctx context.Context, name string) (data.Data, error) {
	return data.ResolvePath(ctx, name, func(head string) (data.Data, error) {
		if d, err := s.f.GetData(head); err == nil {
			return d, nil
		}

		if p := paramNamed(s.f.Outputs(), head); p != nil {
			return p, nil
		}

		if p := paramNamed(s.f.Inputs(), head); p != nil {
			return p, nil
		}

		return nil, errs.New(
			errs.M("%q is not in this activity's data context: neither the "+
				"scope nor the node's own parameters hold it", head),
			errs.C(errorClass, errs.ObjectNotFound))
	})
}

// paramNamed picks the parameter called name, or nil.
func paramNamed(params []*data.Parameter, name string) *data.Parameter {
	for _, p := range params {
		if p.Name() == name {
			return p
		}
	}

	return nil
}

// shaped reports whether the association carries an expression shape and so
// takes the evaluation path rather than the plain copy.
func shaped(a *data.Association) bool {
	return a.Transformation() != nil || len(a.Assignments()) != 0
}

// evaluate runs one of the association's expressions against the frame.
//
// The engine rides the frame (SRD-097 FR-2) and is nil on a transient
// evaluation frame; an association carrying an expression cannot execute
// there, and says so naming itself rather than dereferencing nothing.
func evaluate(
	ctx context.Context,
	f exec.Frame,
	fe data.FormalExpression,
	assocID, owner string,
) (data.Value, error) {
	ee := f.ExpressionEngine()
	if ee == nil {
		return nil, errs.New(
			errs.M("no expression engine wired for %s: association %q "+
				"carries an expression this frame cannot evaluate",
				owner, assocID),
			errs.C(errorClass, errs.InvalidState))
	}

	v, err := ee.Evaluate(ctx, fe, frameSource{f: f})
	if err != nil {
		return nil, errs.New(
			errs.M("association %q of %s: expression evaluation failed",
				assocID, owner),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	if v == nil {
		return nil, errs.New(
			errs.M("association %q of %s: the expression produced no value",
				assocID, owner),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return v, nil
}

// unreadySource names the first source of an expression-bearing
// association that cannot be read, and why — "" when every one is
// available. §10.4.2 gates the WHOLE association on ALL its sources ("if
// any of the sources is in the state of unavailable, the data association
// cannot be executed"), which the plain-copy path expresses by checking
// the one source it reads; a transformation may name none of them and
// still be gated by all.
//
// It returns the reason rather than a bool because the two ways a source
// can be unavailable need different fixes: one is a name the scope does
// not hold (a modeling error — the wrong name, or a data object that is
// not in this scope), the other is a datum that exists and has not been
// produced yet. Collapsing them into "not Ready" sends the reader looking
// for the wrong thing.
func unreadySource(f exec.Frame, a *data.Association) string {
	for _, name := range a.SourceNames() {
		d, err := f.GetData(name)
		if err != nil {
			return fmt.Sprintf("source %q cannot be resolved here (%v)",
				name, err)
		}

		if d.State().Name() != data.ReadyDataState.Name() {
			return fmt.Sprintf("source %q is %s, not Ready",
				name, d.State().Name())
		}
	}

	return ""
}

// applyShape writes the association's expression result into target — the
// value its target element holds. It is the one writer for both shapes, so
// the input half, the output half and the Data Store halves cannot drift
// from each other.
//
// targetName is what the association's target is called; an assignment's to
// path is absolute, and its head must name that target (ADR-011 §2.4) — an
// assignment writing anywhere else would make the association's own
// availability gate, report and movement fact lie about what it touched.
func applyShape(
	ctx context.Context,
	f exec.Frame,
	a *data.Association,
	target data.Value,
	targetName, owner string,
) error {
	if t := a.Transformation(); t != nil {
		v, err := evaluate(ctx, f, t, a.ID(), owner)
		if err != nil {
			return err
		}

		if err := target.Update(ctx, v.Get(ctx)); err != nil {
			return opErr("couldn't write the transformation result of "+owner+" into "+targetName, err)
		}

		return nil
	}

	for i, as := range a.Assignments() {
		if err := applyAssignment(
			ctx, f, a, as, target, targetName, owner, i,
		); err != nil {
			return err
		}
	}

	return nil
}

// applyAssignment evaluates one from→to mapping and writes its result.
func applyAssignment(
	ctx context.Context,
	f exec.Frame,
	a *data.Association,
	as *data.Assignment,
	target data.Value,
	targetName, owner string,
	idx int,
) error {
	head, rest, err := as.ToHead()
	if err != nil {
		return opErr("assignment #"+strconv.Itoa(idx)+" of "+owner, err)
	}

	if head != targetName {
		return errs.New(
			errs.M("assignment #%d of %s writes at %q, but the association's "+
				"target is %q — an assignment writes inside its own "+
				"association's target (ADR-011 §2.4)",
				idx, owner, as.To(), targetName),
			errs.C(errorClass, errs.InvalidObject))
	}

	v, err := evaluate(ctx, f, as.From(), a.ID(), owner)
	if err != nil {
		return err
	}

	// A head-only to replaces the whole value; SetPath refuses an empty
	// path for exactly that reason (ADR-011 §2.9.3).
	if rest == "" {
		if err := target.Update(ctx, v.Get(ctx)); err != nil {
			return opErr("couldn't write assignment #"+strconv.Itoa(idx)+" of "+owner+" into "+targetName, err)
		}

		return nil
	}

	if err := values.SetPath(ctx, target, rest, v); err != nil {
		return opErr("couldn't write assignment #"+strconv.Itoa(idx)+" of "+owner+" at "+as.To(), err)
	}

	return nil
}
