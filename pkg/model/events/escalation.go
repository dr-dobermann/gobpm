package events

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// *****************************************************************************
type Escalation struct {
	foundation.BaseElement

	Name string
	Code string

	Structure *data.ItemDefinition
}

// NewEscalation creates a new Escalation object and returns its pointer.
func NewEscalation(
	id, name, code string,
	item *data.ItemDefinition,
	docs ...*foundation.Documentation,
) *Escalation {
	return &Escalation{
		BaseElement: *foundation.NewBaseElement(id, docs...),
		Name:        name,
		Code:        code,
		Structure:   item,
	}
}

// *****************************************************************************
type EscalationEventDefinition struct {
	Definition

	// If the trigger is an Escalation, then an Escalation payload MAY be
	// provided.
	EscalationRef *Escalation
}
