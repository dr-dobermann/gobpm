package eventproc

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

type EventProcessor interface {
	foundation.Identifyer

	// ProcessEvent processes single event definition by node, it registered
	// in EventProducer.
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
	UnregisterEvents(ep EventProcessor, eDefIds ...string) error

	// UnregisterProcessor unregisters all the event definitions registered by
	// the EventProcessor and the EventProcessor itself.
	UnregisterProcessor(EventProcessor)

	// PropagateEvents gets a list of eventDefinitions and sends them to all
	// EventProcessors registered for this type of EventDefinition.
	PropagateEvents(events ...flow.EventDefinition) error
}
