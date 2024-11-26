package eventhub

import (
	"context"
	"fmt"
	"sync"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

const errorClass = "EVENT_HUB_ERRORS"

type (
	// waitersList holds list of eventWaiter indexed by
	// ItemDefinition id.
	waitersList map[string]eventproc.EventWaiter

	// eventHub processes all registration requests from EventProcessors
	// for specific eventDefinition.
	// On every pair EventProcessor - eventDefinition eventHub creates
	// personal eventWaiter and runs its Service in separate go-routine.
	eventHub struct {
		m sync.RWMutex

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
		return errs.New(
			errs.M("eventHub is already started"),
			errs.C(errorClass, errs.InvalidState))
	}

	eh.started = true

	<-ctx.Done()

	return ctx.Err()
}

// createWaiter creates a new eventWaiter with given EventDefinition and
// EventProcessor.
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

// RegisterEvent registers the EventDefinitions from the single EventProcessor.
func (eh *eventHub) RegisterEvent(
	ep eventproc.EventProcessor,
	eDef flow.EventDefinition,
) error {
	if !eh.started {
		return errs.New(
			errs.M("eventHub isn't started"),
			errs.C(errorClass, errs.InvalidState))
	}

	if ep == nil {
		return errs.New(
			errs.M("empty event processor isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	var p waitersList

	eh.m.RLock()
	p, ok := eh.processors[ep.Id()]
	eh.m.RUnlock()
	if !ok {
		p = *new(waitersList)
	}

	if _, ok := p[eDef.Id()]; ok {
		return fmt.Errorf(
			"eventDefintion %s #%s alredy registered for the EventProcessor #%s",
			eDef.Type(), eDef.Id(), ep.Id())
	}

	w, err := createWaiter(ep, eDef)
	if err != nil {
		return errs.New(
			errs.M("eventWaiter building failed"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.D("event_processor_id", ep.Id()),
			errs.D("event_definition_id", eDef.Id()),
			errs.E(err))
	}

	eh.m.Lock()
	p[eDef.Id()] = w
	eh.m.Unlock()

	return nil
}

// UnregisterEvent removes the registered eventDefintions for single
// EventProcessor.
func (eh *eventHub) UnregisterEvent(
	ep eventproc.EventProcessor,
	eDefId string,
) error {
	if !eh.started {
		return errs.New(
			errs.M("eventHub isn't started"),
			errs.C(errorClass, errs.InvalidState))
	}

	if ep == nil {
		return errs.New(
			errs.M("empty event processor isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if _, ok := eh.processors[ep.Id()]; !ok {
		return errs.New(
			errs.M("couldn't find waiters for eventProcessor"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("event_processor_id", ep.Id()))
	}

	eh.m.RLock()
	w, ok := eh.processors[ep.Id()][eDefId]
	eh.m.RUnlock()

	if !ok {
		return errs.New(
			errs.M("no waiter registered for eventDefiniton"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("event_processor_id", ep.Id()),
			errs.D("event_definition_id", eDefId))
	}

	if err := w.Stop(); err != nil {
		return errs.New(
			errs.M("waiter stopping error"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("event_processor_id", ep.Id()),
			errs.D("event_definition_id", eDefId))
	}

	eh.m.Lock()
	defer eh.m.Unlock()

	delete(eh.processors[ep.Id()], eDefId)

	if len(eh.processors[ep.Id()]) == 0 {
		delete(eh.processors, ep.Id())
	}

	return nil
}

// PropagateEvent sends the event to EventProcessor, registered for this event.
func (eh *eventHub) PropagateEvent(
	ctx context.Context,
	eDef flow.EventDefinition,
) error {
	if !eh.started {
		return errs.New(
			errs.M("eventHub isn't started"),
			errs.C(errorClass, errs.InvalidState))
	}

	sentCnt := 0

	eh.m.RLock()
	defer eh.m.RUnlock()

	for _, wl := range eh.processors {
		if w, ok := wl[eDef.Id()]; ok {
			if err := w.EventProcessor().
				ProcessEvent(ctx, eDef); err != nil {
				return errs.New(
					errs.M("waiter stopping error"),
					errs.C(errorClass, errs.OperationFailed),
					errs.D("event_processor_id", w.EventProcessor().Id()),
					errs.D("event_definition_id", eDef.Id()))
			}

			sentCnt++
		}
	}

	if sentCnt == 0 {
		return fmt.Errorf("waiter isn't found")
	}

	return nil
}

// ----------------------------------------------------------------------------

// interfaces check
var _ eventproc.EventProducer = (*eventHub)(nil)
