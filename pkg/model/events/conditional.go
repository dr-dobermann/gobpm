package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type ConditionalEventDefinition struct {
	definition

	// The Expression might be underspecified and provided in the form of
	// natural language. For executable Processes (isExecutable = true), if the
	// trigger is Conditional, then a FormalExpression MUST be entered.
	condition data.FormalExpression
}

// Type implements the Definition interface.
func (*ConditionalEventDefinition) Type() flow.EventTrigger {
	return flow.TriggerConditional
}

// NewConditionalEventDefinition creates a new ConditionalEventDefinition
// if condition isn't nil. Otherwise it returns error.
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
// ConditionalEventDefinition. If error occured, it fires panic.
func MustConditionalEventDefinition(
	condition data.FormalExpression,
	baseOpts ...options.Option,
) *ConditionalEventDefinition {
	ced, err := NewConditionalEventDefinition(condition, baseOpts...)
	if err != nil {
		panic(err.Error())
	}

	return ced
}

func (ced *ConditionalEventDefinition) Condition() data.FormalExpression {
	return ced.condition
}
