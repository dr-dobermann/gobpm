package events

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// *****************************************************************************
type Escalation struct {
	foundation.BaseElement

	name string
	code string

	structure *data.ItemDefinition
}

// NewEscalation creates a new Escalation object and returns its pointer.
func NewEscalation(
	name, code string,
	item *data.ItemDefinition,
	baseOpts ...foundation.BaseOption,
) *Escalation {
	return &Escalation{
		BaseElement: *foundation.MustBaseElement(baseOpts...),
		name:        name,
		code:        code,
		structure:   item,
	}
}

// *****************************************************************************
type EscalationEventDefinition struct {
	definition

	// If the trigger is an Escalation, then an Escalation payload MAY be
	// provided.
	escalation *Escalation
}

// Type implememnts Definition interface for EscalationEventDefinition.
func (e *EscalationEventDefinition) Type() Trigger {
	return TriggerEscalation
}

// NewEscalationEventDefintion creates a new EscalationEventDefintion and
// returns its pointer.
func NewEscalationEventDefintion(
	id string,
	escalation *Escalation,
	baseOpts ...foundation.BaseOption,
) *EscalationEventDefinition {

	return &EscalationEventDefinition{
		definition: *newDefinition(baseOpts...),
		escalation: escalation,
	}
}
