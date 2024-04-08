package exec

import (
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

type EventProcessor interface {
	foundation.Identifyer

	ProcessEvent(flow.EventDefinition) error
}

type EventProducer interface {
	RegisterEvents(EventProcessor, ...flow.EventDefinition) error
}
