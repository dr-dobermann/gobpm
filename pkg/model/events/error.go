package events

import (
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type ErrorEventDefinition struct {
	definition

	// If the trigger is an Error, then an Error payload MAY be provided.
	err *common.Error
}

// Type implements the Definition interface.
func (*ErrorEventDefinition) Type() Trigger {

	return TriggerError
}

// NewErrorEventDefinition creates a new ErrorEventDefinition and returns
// its pointer.
func NewErrorEventDefinition(
	err *common.Error,
	baseOpts ...options.Option,
) *ErrorEventDefinition {

	return &ErrorEventDefinition{
		definition: *newDefinition(baseOpts...),
		err:        err,
	}
}
