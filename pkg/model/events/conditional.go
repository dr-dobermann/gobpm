package events

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

type ConditionalEventDefinition struct {
	Definition

	// The Expression might be underspecified and provided in the form of
	// natural language. For executable Processes (isExecutable = true), if the
	// trigger is Conditional, then a FormalExpression MUST be entered.
	Condition *data.Expression
}
