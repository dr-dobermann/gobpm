package events

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

type Checker interface {
	// Check tests if the def is equal to the object implemented the
	// function.
	Check(def *Definition) bool
}

// Definition is the base class for define an Event.
type Definition struct {
	foundation.BaseElement
}

// NewDefinition creates a new Event Definition and returns its pointer.
func NewDefinition(id string, docs ...*foundation.Documentation) *Definition {

	return &Definition{
		BaseElement: *foundation.NewBaseElement(id, docs...),
	}
}

// Check implements Checker interface for Definition
func (d *Definition) Check(def *Definition) bool {

	return d.Id() == def.Id()
}
