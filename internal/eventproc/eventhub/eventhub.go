package eventhub

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

type (
	// waitersList holds list of eventWaiter indexed by
	// ItemDefinition id.
	waitersList map[string]eventWaiter

	eventHub struct {
		// processors holds list of waiters started for processing
		// eventDefinitions for a single EventProcessor.
		// processors indexed by eventProcessor id.
		processors map[string]waitersList
	}
)

// New creates a new eventHandler.
func New() (*eventHub, error) {
	return &eventHub{
			processors: map[string]waitersList{},
		},
		nil
}

// --------------------------- eventproc.EventProducer ------------------------

// RegisterEvent registers few EventDefinitions from single EventProcessor.
func (eh *eventHub) RegisterEvent(
	ep eventproc.EventProcessor,
	eDefs ...flow.EventDefinition,
) error {
	return fmt.Errorf("not implemented yet")
}

// UnregisterEvents removes some unfired eventDefintions for single
// EventProcessor.
func (eh *eventHub) UnregisterEvents(
	ep eventproc.EventProcessor,
	eDefIds ...string,
) error {
	return fmt.Errorf("not implemented yet")
}

// UnregisterProcessor removes all unfired evnetDefinition for single
// EventProcessor.
func (eh *eventHub) UnregisterProcessor(ep eventproc.EventProcessor) {
}

// PropagateEvents sends events to EventProcessor, registered those events.
func (eh *eventHub) PropagateEvents(events ...flow.EventDefinition) error {
	return fmt.Errorf("not implemented yet")
}

// ----------------------------------------------------------------------------
