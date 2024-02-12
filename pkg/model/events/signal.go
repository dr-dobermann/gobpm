package events

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// *****************************************************************************
type Signal struct {
	foundation.BaseElement

	Name string

	Structure *data.ItemDefinition
}

// *****************************************************************************
type SignalEventDefinition struct {
	Definition

	// If the trigger is a Signal, then a Signal is provided.
	Signal *Signal
}
