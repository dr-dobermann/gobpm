package events

import (
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
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
	id string,
	err *common.Error,
	docs ...*foundation.Documentation,
) *ErrorEventDefinition {

	return &ErrorEventDefinition{
		definition: *newDefinition(id, docs...),
		err:        err,
	}
}
