package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
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
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "message event definition building error",
				Classes: []string{
					errorClass,
					errs.BulidingFailed,
				},
			}
	}

	return &TerminateEventDefinition{
		definition: *d,
	}, nil
}
