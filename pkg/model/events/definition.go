package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type Definition interface {
	foundation.Identifyer
	foundation.Documentator

	Type() Trigger
}

// definition is the base class for define an Event.
type definition struct {
	foundation.BaseElement
}

// newDefinition creates a new Event Definition and returns its pointer.
func newDefinition(baseOpts ...options.Option) (*definition, error) {
	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "definition build failed",
				Classes: []string{
					errorClass,
					errs.BulidingFailed,
				},
			}
	}

	return &definition{
		BaseElement: *be,
	}, nil
}
