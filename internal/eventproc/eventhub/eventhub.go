package eventhub

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

type (
	// waitersList holds list of eventWaiter indexed by
	// ItemDefinition id.
	waitersList map[string]eventproc.EventWaiter

	// eventHub processes all registration requests from EventProcessors
	// for specific eventDefinition.
	// On every pair EventProcessor - eventDefinition eventHub creates
	// personal eventWaiter and runs its Service in separate go-routine.
	eventHub struct {
		started bool

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

// Run starts main cycle of eventHub.
func (eh *eventHub) Run(ctx context.Context) error {
	if eh.started {
		return fmt.Errorf("already started")
	}

	eh.started = true

	<-ctx.Done()

	return ctx.Err()
}

func createWaiter(
	ep eventproc.EventProcessor,
	eDef flow.EventDefinition,
) (eventproc.EventWaiter, error) {
	var (
		w   eventproc.EventWaiter
		err error
	)

	switch eDef.Type() {
	case flow.TriggerTimer:
		w, err = waiters.NewTimerWaiter(ep, eDef)
	default:
		err = fmt.Errorf(
			"couldn't find builder for eventDefintion #%s of type %s",
			eDef.Id(), eDef.Type())
	}

	return w, err
}

// --------------------------- eventproc.EventProducer ------------------------

// RegisterEvent registers few EventDefinitions from single EventProcessor.
func (eh *eventHub) RegisterEvent(
	ep eventproc.EventProcessor,
	eDefs ...flow.EventDefinition,
) error {
	if !eh.started {
		return fmt.Errorf("event Hub isn't started")
	}

	if ep == nil {
		return fmt.Errorf("no event processor")
	}

	var p waitersList

	p, ok := eh.processors[ep.Id()]
	if !ok {
		p = *new(waitersList)
	}

	for _, ed := range eDefs {
		if _, ok := p[ed.Id()]; ok {
			return fmt.Errorf(
				"eventDefintion %s #%s alredy registered for the EventProcessor #%s",
				ed.Type(), ed.Id(), ep.Id())
		}
	}

	return nil
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

// PropagateEvents sends events to EventProcessor, registered for those events.
func (eh *eventHub) PropagateEvents(events ...flow.EventDefinition) error {
	return fmt.Errorf("not implemented yet")
}

// ----------------------------------------------------------------------------
