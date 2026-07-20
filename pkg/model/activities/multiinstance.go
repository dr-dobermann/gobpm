package activities

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// MultiInstanceLoopCharacteristics is a Multi-Instance marker (BPMN §13.3.7):
// the activity runs a fixed number of times, decided once at activation from
// loopCardinality (an integer expression) or the size of the loopDataInputRef
// collection. This slice implements the sequential shape (isSequential); the
// data references are scope-datum names, resolved by name like any per-scope
// datum. behavior/ComplexBehaviorDefinition and parallel execution land later
// (SRD-056).
type MultiInstanceLoopCharacteristics struct {
	loopCardinality     data.FormalExpression
	completionCondition data.FormalExpression
	loopDataInputRef    string
	loopDataOutputRef   string
	inputDataItem       string
	outputDataItem      string
	foundation.BaseElement
	isSequential bool
}

// isLoopCharacteristics seals MultiInstanceLoopCharacteristics as a
// LoopCharacteristics.
func (*MultiInstanceLoopCharacteristics) isLoopCharacteristics() {}

// MultiInstanceOption configures a MultiInstanceLoopCharacteristics at
// construction.
type MultiInstanceOption func(*MultiInstanceLoopCharacteristics) error

// WithSequential runs the instances one after another (BPMN §13.3.7
// isSequential). Without it a Multi-Instance is parallel — which this slice does
// not yet execute (SRD-056).
func WithSequential() MultiInstanceOption {
	return func(mi *MultiInstanceLoopCharacteristics) error {
		mi.isSequential = true

		return nil
	}
}

// WithCardinality fixes the instance count to an integer expression, evaluated
// once at activation. Mutually exclusive with WithInputCollection.
func WithCardinality(expr data.FormalExpression) MultiInstanceOption {
	return func(mi *MultiInstanceLoopCharacteristics) error {
		if expr == nil {
			return errs.New(
				errs.M("WithCardinality: a nil cardinality expression isn't "+
					"allowed"),
				errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
		}

		mi.loopCardinality = expr

		return nil
	}
}

// WithInputCollection drives the instance count from a collection: ref names the
// input collection datum in scope, item names the per-instance datum bound to
// element i. Mutually exclusive with WithCardinality.
func WithInputCollection(ref, item string) MultiInstanceOption {
	return func(mi *MultiInstanceLoopCharacteristics) error {
		if ref == "" || item == "" {
			return errs.New(
				errs.M("WithInputCollection: both the collection ref and the "+
					"per-instance item name are required"),
				errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
		}

		mi.loopDataInputRef = ref
		mi.inputDataItem = item

		return nil
	}
}

// WithOutputCollection assembles each instance's item into the ref collection:
// ref names the output collection datum, item names the per-instance output
// datum read from the instance.
func WithOutputCollection(ref, item string) MultiInstanceOption {
	return func(mi *MultiInstanceLoopCharacteristics) error {
		if ref == "" || item == "" {
			return errs.New(
				errs.M("WithOutputCollection: both the collection ref and the "+
					"per-instance item name are required"),
				errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
		}

		mi.loopDataOutputRef = ref
		mi.outputDataItem = item

		return nil
	}
}

// WithCompletionCondition ends the activity early: the boolean expression is
// evaluated after each instance completes; true stops the remaining instances.
func WithCompletionCondition(expr data.FormalExpression) MultiInstanceOption {
	return func(mi *MultiInstanceLoopCharacteristics) error {
		if expr == nil {
			return errs.New(
				errs.M("WithCompletionCondition: a nil condition isn't allowed"),
				errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
		}

		mi.completionCondition = expr

		return nil
	}
}

// NewMultiInstance creates a MultiInstanceLoopCharacteristics from options. It
// requires exactly one cardinality source (WithCardinality XOR
// WithInputCollection), an integer cardinality expression, and — when present —
// a boolean completionCondition.
func NewMultiInstance(
	opts ...MultiInstanceOption,
) (*MultiInstanceLoopCharacteristics, error) {
	mi := &MultiInstanceLoopCharacteristics{
		BaseElement: *foundation.MustBaseElement(),
	}

	for _, opt := range opts {
		if err := opt(mi); err != nil {
			return nil, err
		}
	}

	hasCardinality := mi.loopCardinality != nil
	hasCollection := mi.loopDataInputRef != ""

	switch {
	case hasCardinality == hasCollection:
		return nil, errs.New(
			errs.M("NewMultiInstance: exactly one cardinality source is "+
				"required — WithCardinality or WithInputCollection, not both"),
			errs.C(errorClass, errs.InvalidParameter))

	case hasCardinality && mi.loopCardinality.ResultType() != resultTypeInt:
		return nil, errs.New(
			errs.M("NewMultiInstance: loopCardinality must be an integer "+
				"expression, got %q", mi.loopCardinality.ResultType()),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if mi.completionCondition != nil &&
		mi.completionCondition.ResultType() != resultTypeBool {
		return nil, errs.New(
			errs.M("NewMultiInstance: completionCondition must be a boolean "+
				"expression, got %q", mi.completionCondition.ResultType()),
			errs.C(errorClass, errs.InvalidParameter))
	}

	return mi, nil
}

// IsSequential reports whether the instances run one after another (§13.3.7).
func (mi *MultiInstanceLoopCharacteristics) IsSequential() bool {
	return mi.isSequential
}

// LoopCardinality returns the integer cardinality expression, or nil when the
// count is collection-driven.
func (mi *MultiInstanceLoopCharacteristics) LoopCardinality() data.FormalExpression {
	return mi.loopCardinality
}

// CompletionCondition returns the early-completion boolean expression, or nil.
func (mi *MultiInstanceLoopCharacteristics) CompletionCondition() data.FormalExpression {
	return mi.completionCondition
}

// LoopDataInputRef returns the input collection datum name, or "" when the count
// is cardinality-driven.
func (mi *MultiInstanceLoopCharacteristics) LoopDataInputRef() string {
	return mi.loopDataInputRef
}

// LoopDataOutputRef returns the output collection datum name, or "" when the
// activity assembles no output.
func (mi *MultiInstanceLoopCharacteristics) LoopDataOutputRef() string {
	return mi.loopDataOutputRef
}

// InputDataItem returns the per-instance input datum name.
func (mi *MultiInstanceLoopCharacteristics) InputDataItem() string {
	return mi.inputDataItem
}

// OutputDataItem returns the per-instance output datum name.
func (mi *MultiInstanceLoopCharacteristics) OutputDataItem() string {
	return mi.outputDataItem
}
