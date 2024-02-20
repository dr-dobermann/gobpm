package events

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

type TerminateEventDefinition struct {
	definition
}

// Type implements the Definition interface.
func (*TerminateEventDefinition) Type() Trigger {

	return TriggerError
}

// NewTerminateEventDefinition creates a new TerminateEventDefinition
// and returns its pointer.
func NewTerminateEventDefinition(
	baseOpts ...foundation.BaseOption,
) *TerminateEventDefinition {

	return &TerminateEventDefinition{
		definition: *newDefinition(baseOpts...),
	}
}
