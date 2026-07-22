package activities

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// MultiInstanceBehavior governs whether a Multi-Instance activity throws an
// event as its instances complete (BPMN §13.3.7, ADR-025 §2.8).
type MultiInstanceBehavior string

const (
	// BehaviorAll (the default) throws no event — the common, zero-cost case.
	BehaviorAll MultiInstanceBehavior = "all"
	// BehaviorNone throws noneBehaviorEventRef for EVERY instance completion.
	BehaviorNone MultiInstanceBehavior = "none"
	// BehaviorOne throws oneBehaviorEventRef once, on the FIRST completion.
	BehaviorOne MultiInstanceBehavior = "one"
	// BehaviorComplex consults the complexBehaviorDefinition entries on each
	// completion, throwing the event of each whose condition holds.
	BehaviorComplex MultiInstanceBehavior = "complex"
)

// ComplexBehaviorDefinition drives BehaviorComplex (BPMN §ComplexBehaviorDefinition):
// on each instance completion its condition is evaluated, and when true its event
// is thrown (catchable on the Multi-Instance activity's boundary).
type ComplexBehaviorDefinition struct {
	condition data.FormalExpression
	event     *events.ImplicitThrowEvent
	foundation.BaseElement
}

// NewComplexBehaviorDefinition builds a complex-behavior entry. The boolean
// condition and the thrown event are both required.
func NewComplexBehaviorDefinition(
	condition data.FormalExpression, event *events.ImplicitThrowEvent,
) (*ComplexBehaviorDefinition, error) {
	if condition == nil || event == nil {
		return nil, errs.New(
			errs.M("NewComplexBehaviorDefinition: both a condition and an event "+
				"are required"),
			errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
	}

	if condition.ResultType() != resultTypeBool {
		return nil, errs.New(
			errs.M("NewComplexBehaviorDefinition: condition must be a boolean "+
				"expression, got %q", condition.ResultType()),
			errs.C(errorClass, errs.InvalidParameter))
	}

	return &ComplexBehaviorDefinition{
		BaseElement: *foundation.MustBaseElement(),
		condition:   condition,
		event:       event,
	}, nil
}

// Condition returns the boolean expression evaluated on each instance completion.
func (c *ComplexBehaviorDefinition) Condition() data.FormalExpression {
	return c.condition
}

// Event returns the event thrown when the condition holds.
func (c *ComplexBehaviorDefinition) Event() *events.ImplicitThrowEvent {
	return c.event
}

// MultiInstanceLoopCharacteristics is a Multi-Instance marker (BPMN §13.3.7):
// the activity runs a fixed number of times, decided once at activation from
// loopCardinality (an integer expression) or the size of the loopDataInputRef
// collection. This slice implements the sequential shape (isSequential); the
// data references are scope-datum names, resolved by name like any per-scope
// datum. behavior/ComplexBehaviorDefinition and parallel execution land later
// (SRD-056).
type MultiInstanceLoopCharacteristics struct {
	loopCardinality           data.FormalExpression
	completionCondition       data.FormalExpression
	noneBehaviorEventRef      flow.EventDefinition
	oneBehaviorEventRef       flow.EventDefinition
	complexBehaviorDefinition []*ComplexBehaviorDefinition
	loopDataInputRef          string
	loopDataOutputRef         string
	inputDataItem             string
	outputDataItem            string
	behavior                  MultiInstanceBehavior
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

// WithBehavior sets the event-throwing behavior (BPMN §13.3.7, ADR-025 §2.8);
// the default is BehaviorAll (no event). See BehaviorNone/One/Complex.
func WithBehavior(b MultiInstanceBehavior) MultiInstanceOption {
	return func(mi *MultiInstanceLoopCharacteristics) error {
		switch b {
		case BehaviorAll, BehaviorNone, BehaviorOne, BehaviorComplex:
			mi.behavior = b

			return nil

		default:
			return errs.New(
				errs.M("WithBehavior: unknown Multi-Instance behavior %q", b),
				errs.C(errorClass, errs.InvalidParameter))
		}
	}
}

// WithNoneBehaviorEvent sets the event thrown on every instance completion
// (BehaviorNone).
func WithNoneBehaviorEvent(def flow.EventDefinition) MultiInstanceOption {
	return func(mi *MultiInstanceLoopCharacteristics) error {
		if def == nil {
			return errs.New(
				errs.M("WithNoneBehaviorEvent: a nil event definition isn't "+
					"allowed"),
				errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
		}

		mi.noneBehaviorEventRef = def

		return nil
	}
}

// WithOneBehaviorEvent sets the event thrown once, on the first instance
// completion (BehaviorOne).
func WithOneBehaviorEvent(def flow.EventDefinition) MultiInstanceOption {
	return func(mi *MultiInstanceLoopCharacteristics) error {
		if def == nil {
			return errs.New(
				errs.M("WithOneBehaviorEvent: a nil event definition isn't "+
					"allowed"),
				errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
		}

		mi.oneBehaviorEventRef = def

		return nil
	}
}

// WithComplexBehavior sets the complex-behavior definitions consulted on each
// instance completion (BehaviorComplex); each carries a condition and the event
// thrown when it holds.
func WithComplexBehavior(defs ...*ComplexBehaviorDefinition) MultiInstanceOption {
	return func(mi *MultiInstanceLoopCharacteristics) error {
		for _, d := range defs {
			if d == nil {
				return errs.New(
					errs.M("WithComplexBehavior: a nil definition isn't allowed"),
					errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
			}
		}

		mi.complexBehaviorDefinition = defs

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

	if mi.behavior == "" {
		mi.behavior = BehaviorAll
	}

	if err := validateBehavior(mi); err != nil {
		return nil, err
	}

	return mi, nil
}

// behaviorSource reports whether the Multi-Instance carries the one event source
// a non-All behavior requires — a data table, not a per-mode branch.
var behaviorSource = map[MultiInstanceBehavior]func(*MultiInstanceLoopCharacteristics) bool{
	BehaviorNone:    func(mi *MultiInstanceLoopCharacteristics) bool { return mi.noneBehaviorEventRef != nil },
	BehaviorOne:     func(mi *MultiInstanceLoopCharacteristics) bool { return mi.oneBehaviorEventRef != nil },
	BehaviorComplex: func(mi *MultiInstanceLoopCharacteristics) bool { return len(mi.complexBehaviorDefinition) > 0 },
}

// validateBehavior enforces the behavior⇄ref consistency (ADR-025 §2.8): each
// non-All mode requires exactly its own event source (no other set), and
// BehaviorAll forbids all.
func validateBehavior(mi *MultiInstanceLoopCharacteristics) error {
	sources := 0
	if mi.noneBehaviorEventRef != nil {
		sources++
	}

	if mi.oneBehaviorEventRef != nil {
		sources++
	}

	if len(mi.complexBehaviorDefinition) > 0 {
		sources++
	}

	if mi.behavior == BehaviorAll {
		if sources > 0 {
			return errs.New(
				errs.M("NewMultiInstance: BehaviorAll throws no event — no "+
					"behavior event may be set"),
				errs.C(errorClass, errs.InvalidParameter))
		}

		return nil
	}

	if sources != 1 || !behaviorSource[mi.behavior](mi) {
		return errs.New(
			errs.M("NewMultiInstance: %s behavior requires exactly its own "+
				"event source", mi.behavior),
			errs.C(errorClass, errs.InvalidParameter))
	}

	return nil
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

// Behavior returns the event-throwing behavior (BehaviorAll by default).
func (mi *MultiInstanceLoopCharacteristics) Behavior() MultiInstanceBehavior {
	return mi.behavior
}

// NoneBehaviorEvent returns the event thrown on every completion (BehaviorNone),
// or nil.
func (mi *MultiInstanceLoopCharacteristics) NoneBehaviorEvent() flow.EventDefinition {
	return mi.noneBehaviorEventRef
}

// OneBehaviorEvent returns the event thrown on the first completion (BehaviorOne),
// or nil.
func (mi *MultiInstanceLoopCharacteristics) OneBehaviorEvent() flow.EventDefinition {
	return mi.oneBehaviorEventRef
}

// ComplexBehavior returns the complex-behavior definitions (BehaviorComplex), or
// nil.
func (mi *MultiInstanceLoopCharacteristics) ComplexBehavior() []*ComplexBehaviorDefinition {
	return mi.complexBehaviorDefinition
}
