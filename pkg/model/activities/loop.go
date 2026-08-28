package activities

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// LoopCharacteristics marks an activity as iterating — running its inner
// activity more than once (ADR-025 §2.1). The concrete kind (Standard Loop or,
// later, Multi-Instance) selects the execution mechanism (ADR-025 §2.2). An
// activity carries at most one; the interface is sealed to this package via the
// unexported marker method.
type LoopCharacteristics interface {
	isLoopCharacteristics()
}

// StandardLoopCharacteristics is a sequential, condition-driven loop (BPMN
// §13.3.6): the inner activity runs repeatedly while loopCondition holds,
// optionally bounded by loopMaximum. testBefore selects a pre-tested (while) or
// post-tested (do-while, the default) loop.
type StandardLoopCharacteristics struct {
	loopCondition data.FormalExpression
	loopMaximum   *int

	// result is the declared reading of the passes' results, nil for the
	// last-wins default (ADR-025 §2.6.1).
	result *ResultStrategy

	foundation.BaseElement
	testBefore bool
}

// isLoopCharacteristics seals StandardLoopCharacteristics as a
// LoopCharacteristics.
func (*StandardLoopCharacteristics) isLoopCharacteristics() {}

// StandardLoopOption configures a StandardLoopCharacteristics at construction.
type StandardLoopOption func(*StandardLoopCharacteristics) error

// WithTestBefore makes the loop pre-tested (a while loop): loopCondition is
// checked before each run, so zero iterations are possible. Without it the loop
// is post-tested (a do-while, the BPMN §13.3.6 default): it runs once, then
// tests.
func WithTestBefore() StandardLoopOption {
	return func(sl *StandardLoopCharacteristics) error {
		sl.testBefore = true

		return nil
	}
}

// WithLoopMaximum caps the loop at n iterations regardless of loopCondition. n
// must be positive; a zero or negative cap is rejected (to run zero times, use a
// pre-tested loop with a false condition).
func WithLoopMaximum(n int) StandardLoopOption {
	return func(sl *StandardLoopCharacteristics) error {
		if n <= 0 {
			return errs.New(
				errs.M("WithLoopMaximum: the loop maximum must be positive, "+
					"got %d", n),
				errs.C(errorClass, errs.InvalidParameter))
		}

		m := n
		sl.loopMaximum = &m

		return nil
	}
}

// NewStandardLoop creates a StandardLoopCharacteristics from a boolean
// loopCondition and options. loopCondition must be non-nil and evaluate to a
// bool (BPMN §13.3.6). It returns an error on an invalid condition or option.
func NewStandardLoop(
	loopCondition data.FormalExpression,
	opts ...StandardLoopOption,
) (*StandardLoopCharacteristics, error) {
	if loopCondition == nil {
		return nil, errs.New(
			errs.M("NewStandardLoop: a nil loopCondition isn't allowed"),
			errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
	}

	if loopCondition.ResultType() != resultTypeBool {
		return nil, errs.New(
			errs.M("NewStandardLoop: loopCondition must be a boolean "+
				"expression, got %q", loopCondition.ResultType()),
			errs.C(errorClass, errs.InvalidParameter))
	}

	sl := &StandardLoopCharacteristics{
		// The base element is built from zero options, so it cannot fail —
		// MustBaseElement documents that invariant and keeps the constructor's
		// error surface to the two real cases above (nil / non-bool condition).
		BaseElement:   foundation.EmptyBaseElement(),
		loopCondition: loopCondition,
	}

	for _, opt := range opts {
		if err := opt(sl); err != nil {
			return nil, err
		}
	}

	return sl, nil
}

// LoopCondition returns the boolean continuation expression.
func (sl *StandardLoopCharacteristics) LoopCondition() data.FormalExpression {
	return sl.loopCondition
}

// TestBefore reports whether the loop is pre-tested (a while loop). False is a
// post-tested do-while loop (the BPMN §13.3.6 default).
func (sl *StandardLoopCharacteristics) TestBefore() bool {
	return sl.testBefore
}

// LoopMaximum returns the iteration cap and whether one is set. When ok is
// false the loop is unbounded (subject only to loopCondition).
func (sl *StandardLoopCharacteristics) LoopMaximum() (int, bool) {
	if sl.loopMaximum == nil {
		return 0, false
	}

	return *sl.loopMaximum, true
}

// WithLoopResultArray declares that the passes' results are indexed by pass
// ORDINAL and published under name at completion (ADR-025 §2.6.1).
//
// An engine extension: BPMN gives a Standard Loop no output aggregation at
// all, only a Multi-Instance.
func WithLoopResultArray(name string) StandardLoopOption {
	return func(sl *StandardLoopCharacteristics) error {
		return sl.declareResult(ResultArray, name, nil)
	}
}

// WithLoopResultMap declares that the passes' results are keyed by key,
// evaluated in the completing pass's own frame — see WithResultMap.
func WithLoopResultMap(
	name string, key data.FormalExpression, opts ...MapOption,
) StandardLoopOption {
	return func(sl *StandardLoopCharacteristics) error {
		return sl.declareResult(ResultMap, name, key, opts...)
	}
}

// WithLoopResultReduce names the accumulating default under name — see
// WithResultReduce. For a Standard Loop the fold is the shape's whole point,
// which is why naming it matters more here than anywhere.
func WithLoopResultReduce(name string) StandardLoopOption {
	return func(sl *StandardLoopCharacteristics) error {
		return sl.declareResult(ResultReduce, name, nil)
	}
}

// declareResult records the one strategy this loop may declare.
func (sl *StandardLoopCharacteristics) declareResult(
	kind ResultKind, name string, key data.FormalExpression, opts ...MapOption,
) error {
	r, err := newResultStrategy(kind, name, key, opts...)
	if err != nil {
		return err
	}

	if sl.result != nil {
		return errSecondStrategy(sl.result.kind, kind)
	}

	sl.result = r

	return nil
}

// Result is the declared result strategy, or nil for the last-wins default.
func (sl *StandardLoopCharacteristics) Result() *ResultStrategy {
	return sl.result
}
