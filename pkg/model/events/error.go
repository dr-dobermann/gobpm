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
			errs.New(
				errs.M("empty error object isn't allowed"),
				errs.C(errorClass, errs.BulidingFailed))
	}

	d, err := newDefinition(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &ErrorEventDefinition{
		definition: *d,
		err:        cErr,
	}, nil
}

// Error returns the ErrorEventDefinition error structure.
func (eed *ErrorEventDefinition) Error() *common.Error {
	return eed.err
}
