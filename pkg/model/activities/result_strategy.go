package activities

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// ResultKind names how an iteration's instances' results are assembled
// (ADR-025 §2.6.1).
type ResultKind string

const (
	// ResultArray indexes results by ORDINAL: slot i holds instance i's
	// output, whatever order the instances completed in.
	//
	// For a Multi-Instance this is the standard's own `loopDataOutputRef`
	// assembly (§13.3.7). For a Standard Loop it is an engine extension —
	// the standard gives a loop no output aggregation at all.
	ResultArray ResultKind = "array"

	// ResultMap keys results by a per-instance expression, evaluated in that
	// instance's frame at its completion. An engine extension for both
	// shapes: BPMN's Multi-Instance output is an ordered collection, never a
	// keyed one.
	ResultMap ResultKind = "map"

	// ResultReduce names the accumulating default: each instance's writes
	// land in the enclosing scope and a later one replaces an earlier.
	//
	// It adds no assembly, because it IS what happens without a declaration.
	// It exists so a model can SAY it is relying on the fold — a sequential
	// iteration reading what the previous pass committed is the useful
	// default, and an implicit fold is a thing readers rediscover by
	// experiment.
	ResultReduce ResultKind = "reduce"
)

// ResultStrategy is a declared reading of an iteration's results.
//
// Nil means the default of ADR-025 §2.6.1: last write wins, which is a fold
// for a sequential shape and order-dependent for a parallel one. The declared
// strategies exist so a model that needs every instance's result can say so
// and get a deterministic one.
type ResultStrategy struct {
	// key is the map strategy's per-instance expression, nil for the others.
	key data.FormalExpression

	// name is where the assembled result is published.
	name string

	kind ResultKind

	// errorOnKeyRewrite makes a duplicate map key a fault rather than an
	// overwrite (§2.6.1).
	errorOnKeyRewrite bool
}

// Kind reports how the results are assembled.
func (r *ResultStrategy) Kind() ResultKind { return r.kind }

// Name is where the assembled result is published.
func (r *ResultStrategy) Name() string { return r.name }

// Key is the map strategy's per-instance key expression, nil for the others.
func (r *ResultStrategy) Key() data.FormalExpression { return r.key }

// ErrorOnKeyRewrite reports whether a duplicate map key faults.
//
// False is not "the collision is fine": it is the last-wins default, and the
// loss is detectable rather than silent — RUNTIME/ITERATIONS publishes the
// instance total, so a map holding fewer entries than that says so.
func (r *ResultStrategy) ErrorOnKeyRewrite() bool { return r.errorOnKeyRewrite }

// MapOption tunes the map strategy.
type MapOption func(*ResultStrategy) error

// ErrorOnKeyRewrite makes a duplicate map key a fault, naming both ordinals
// and the key, rather than letting the later instance overwrite the earlier.
//
// For a model where a collision is a modeling error — a fan-out over
// participants who must each answer once — the overwrite is the bug, not the
// remedy.
func ErrorOnKeyRewrite() MapOption {
	return func(r *ResultStrategy) error {
		r.errorOnKeyRewrite = true

		return nil
	}
}

// newResultStrategy validates one declaration.
func newResultStrategy(
	kind ResultKind, name string, key data.FormalExpression, opts ...MapOption,
) (*ResultStrategy, error) {
	if name == "" {
		return nil, errs.New(
			errs.M("a result strategy needs the name it publishes under"),
			errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
	}

	if kind == ResultMap && key == nil {
		return nil, errs.New(
			errs.M("WithResultMap: a nil key expression isn't allowed — the "+
				"key is what says which instance's result went where"),
			errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
	}

	r := ResultStrategy{kind: kind, name: name, key: key}

	for _, o := range opts {
		if o == nil {
			return nil, errs.New(
				errs.M("WithResultMap: a nil MapOption isn't allowed"),
				errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
		}

		if err := o(&r); err != nil {
			return nil, err
		}
	}

	return &r, nil
}

// errSecondStrategy refuses a second declaration on one activity.
//
// The three are alternative READINGS of the same instances' results, not
// stages of one pipeline: an array and a map disagree about what a result is
// indexed by, and reduce says there is nothing to assemble. Composing them has
// no meaning to give, so the model is refused where it is written rather than
// resolved by a precedence rule nobody could remember.
func errSecondStrategy(had, got ResultKind) error {
	return errs.New(
		errs.M("an activity declares ONE result strategy: %q is already "+
			"declared, and %q would be a second reading of the same "+
			"instances' results", string(had), string(got)),
		errs.C(errorClass, errs.InvalidParameter))
}
