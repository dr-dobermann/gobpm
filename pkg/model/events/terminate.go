package events

import (
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type TerminateEventDefinition struct {
	definition
}

// Type implements the Definition interface.
func (*TerminateEventDefinition) Type() Trigger {

	return TriggerTerminate
}

// NewTerminateEventDefinition creates a new TerminateEventDefinition
// and returns its pointer.
func NewTerminateEventDefinition(
	baseOpts ...options.Option,
) (*TerminateEventDefinition, error) {
	d, err := newDefinition(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &TerminateEventDefinition{
		definition: *d,
	}, nil
}
