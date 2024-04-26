package exec

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

type EventProcessor interface {
	foundation.Identifyer

	ProcessEvent(context.Context, flow.EventDefinition) error
}

type EventProducer interface {
	// RegisterEvents register group of event definition the EventProcessor
	// is waiting for. Once the EventProducer got the event with event
	// definition matched with registered event definition Id, it calls the
	// registered EventProcessor with given event definition.
	RegisterEvents(EventProcessor, ...flow.EventDefinition) error

	// UnregisterEvents removes event definition to EventProcessor link from
	// EventProducer.
	UnregisterEvents(EventProcessor, ...flow.EventDefinition) error
}
