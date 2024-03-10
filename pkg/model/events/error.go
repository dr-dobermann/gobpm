package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
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
	cErr *common.Error,
	baseOpts ...options.Option,
) (*ErrorEventDefinition, error) {
	if cErr == nil {
		return nil,
			&errs.ApplicationError{
				Message: "empty error object isn't allowed",
				Classes: []string{
					errorClass,
					errs.BulidingFailed,
				},
			}
	}

	d, err := newDefinition(baseOpts...)
	if err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "error event definition building failed",
				Classes: []string{
					errorClass,
					errs.BulidingFailed,
				},
			}
	}

	return &ErrorEventDefinition{
		definition: *d,
		err:        cErr,
	}, nil
}
