package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

type ConditionalEventDefinition struct {
	definition

	// The Expression might be underspecified and provided in the form of
	// natural language. For executable Processes (isExecutable = true), if the
	// trigger is Conditional, then a FormalExpression MUST be entered.
	condition *data.Expression
}

// Type implements the Definition interface.
func (*ConditionalEventDefinition) Type() Trigger {

	return TriggerConditional
}

// NewConditionalEventDefinition creates a new ConditionalEventDefinition
// if condition isn't nil. Otherwise it returns error.
func NewConditionalEventDefinition(
	id string,
	condition *data.Expression,
	docs ...*foundation.Documentation,
) (*ConditionalEventDefinition, error) {
	if condition == nil {
		return nil,
			&errs.ApplicationError{
				Message: "condition couldn't be empty",
				Classes: []string{
					eventErrorClass,
					errs.InvalidParameter,
				},
			}
	}

	return &ConditionalEventDefinition{
		definition: *newDefinition(id, docs...),
		condition:  condition,
	}, nil
}

// MustConditionalEventDefinition tries to create a new
// ConditionalEventDefinition. If error occured, it fires panic.
func MustConditionalEventDefinition(
	id string,
	condition *data.Expression,
	docs ...*foundation.Documentation,
) *ConditionalEventDefinition {
	ced, err := NewConditionalEventDefinition(id, condition, docs...)
	if err != nil {
		panic(err.Error())
	}

	return ced
}
