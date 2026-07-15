package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// ConditionalEventDefinition represents a conditional event definition.
type ConditionalEventDefinition struct {
	condition data.FormalExpression
	definition
}

// Type implements the Definition interface.
func (*ConditionalEventDefinition) Type() flow.EventTrigger {
	return flow.TriggerConditional
}

// NewConditionalEventDefinition creates a new ConditionalEventDefinition
// if condition isn't nil and evaluates to a boolean. Otherwise it returns
// error.
func NewConditionalEventDefinition(
	condition data.FormalExpression,
	baseOpts ...options.Option,
) (*ConditionalEventDefinition, error) {
	if condition == nil {
		return nil,
			errs.New(
				errs.M("condition couldn't be empty"),
				errs.C(errorClass, errs.InvalidParameter))
	}

	// A conditional event fires on a status becoming true (BPMN Table
	// 10.84), so a non-boolean condition is meaningless — rejected at model
	// build, before it could reach the runtime (the TimerEventDefinition
	// ResultType idiom).
	if rt := condition.ResultType(); rt != "bool" {
		return nil,
			errs.New(
				errs.M("condition expression result isn't boolean"),
				errs.C(errorClass, errs.InvalidObject),
				errs.D("expected_type", "bool"),
				errs.D("expr_type", rt))
	}

	d, err := newDefinition(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &ConditionalEventDefinition{
		definition: *d,
		condition:  condition,
	}, nil
}

// MustConditionalEventDefinition tries to create a new
// ConditionalEventDefinition. If error occurred, it fires panic.
func MustConditionalEventDefinition(
	condition data.FormalExpression,
	baseOpts ...options.Option,
) *ConditionalEventDefinition {
	ced, err := NewConditionalEventDefinition(condition, baseOpts...)
	if err != nil {
		errs.Panic(err.Error())
	}

	return ced
}

// Condition returns the formal expression condition.
func (ced *ConditionalEventDefinition) Condition() data.FormalExpression {
	return ced.condition
}
