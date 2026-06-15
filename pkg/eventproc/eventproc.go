// Package eventproc holds the public event-production contracts a node
// implements/consumes: EventProcessor (a node that handles a fired event) and
// EventProducer (registers processors and propagates events). The EventHub and
// EventWaiter implementations stay internal (ADR-012 v.1).
package eventproc

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// EventProcessor handles a single fired event.
type EventProcessor interface {
	foundation.Identifyer

	// ProcessEvent processes a single event definition by the node it is
	// registered for in an EventProducer.
	ProcessEvent(context.Context, flow.EventDefinition) error
}

// EventProducer registers event processors that expect events and propagates
// fired events to them.
type EventProducer interface {
	// RegisterEvent registers the EventDefinition the EventProcessor is
	// waiting for. Once the EventProducer gets an event whose definition id
	// matches a registered one, it calls the registered EventProcessor with
	// that event definition.
	RegisterEvent(EventProcessor, flow.EventDefinition) error

	// UnregisterEvent removes the event-definition→EventProcessor link from
	// the EventProducer.
	UnregisterEvent(ep EventProcessor, eDefID string) error

	// PropagateEvent sends a fired throw event's eventDefinition up the chain
	// of EventProducers.
	PropagateEvent(context.Context, flow.EventDefinition) error
}
