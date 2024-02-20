package events

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

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
func newDefinition(id string, docs ...*foundation.Documentation) *definition {

	return &definition{
		BaseElement: *foundation.NewBaseElement(id, docs...),
	}
}
