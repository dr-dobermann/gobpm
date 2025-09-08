package eventhub

import (
	"context"
	"sync"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

const errorClass = "EVENT_HUB_ERRORS"

type (
	// eventHub processes all registration requests from EventProcessors
	// for specific eventDefinition.
	// On every pair EventProcessor - eventDefinition eventHub creates
	// personal eventWaiter and runs its Service in separate go-routine.
	eventHub struct {
		started bool

		// waiters holds a list of waiters started for processing
		// eventDefinitions for a list of EventProcessors.
		// waiters indexed by event definition id.
		waiters map[string]eventproc.EventWaiter

		events []flow.EventDefinition

		ctx context.Context

		m sync.RWMutex
	}
)

// New creates a new eventHandler.
func New() (*eventHub, error) {
	return &eventHub{
			waiters: map[string]eventproc.EventWaiter{},
			events:  []flow.EventDefinition{},
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
	eh.ctx = ctx

	<-ctx.Done()

	return ctx.Err()
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

	eh.m.RLock()
	w, ok := eh.waiters[eDef.Id()]
	eh.m.RUnlock()
	if ok {
		if err := w.AddEventProcessor(ep); err != nil {
			return errs.New(
				errs.M("couldn't add event processor to waiter"),
				errs.C(errorClass, errs.OperationFailed),
				errs.D("waiter_id", w.Id()),
				errs.D("event_definition_id", eDef.Id()),
				errs.D("event_definition_type", eDef.Type()),
				errs.D("event_processor_id", ep.Id()))
		}

		return nil
	}

	w, err := waiters.CreateWaiter(eh, ep, eDef)
	if err != nil {
		return errs.New(
			errs.M("eventWaiter building failed"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.D("event_processor_id", ep.Id()),
			errs.D("event_definition_id", eDef.Id()),
			errs.E(err))
	}

	eh.m.Lock()
	eh.waiters[eDef.Id()] = w
	eh.m.Unlock()

	w.Service(eh.ctx)

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

	eh.m.RLock()
	w, ok := eh.waiters[eDefId]
	eh.m.RUnlock()

	if !ok {
		return errs.New(
			errs.M("couldn't find waiter for the event definition"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("event_definition_id", eDefId))
	}

	if err := w.RemoveEventProcessor(ep); err != nil {
		return errs.New(
			errs.M("couldn't remove event processor from waiter"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("event_waiter_id", w.Id()),
			errs.D("event_processor_id", ep.Id()),
			errs.D("event_definition_id", eDefId),
			errs.E(err))
	}

	if len(w.EventProcessors()) == 0 {
		if w.State() == eventproc.WSRunned {
			if err := w.Stop(); err != nil {
				return errs.New(
					errs.M("waiter stop failed"),
					errs.C(errorClass, errs.OperationFailed),
					errs.D("waiter_id", w.Id()),
					errs.D("event_definition_idf", w.EventDefinition().Id()),
					errs.D("event_definition_type", w.EventDefinition().Type()))
			}
		}

		return eh.RemoveWaiter(w)
	}

	return nil
}

// PropagateEvent sends a fired throw event's eventDefinition
// up to chain of EventProducers.
//
// Since the eventHub is the last event producer in the chain
// it puts the event into event queue for further processing by
// the appropriate waiter.
func (eh *eventHub) PropagateEvent(
	ctx context.Context,
	eDef flow.EventDefinition,
) error {
	if !eh.started {
		return errs.New(
			errs.M("eventHub isn't started"),
			errs.C(errorClass, errs.InvalidState))
	}

	eh.m.RLock()
	w, ok := eh.waiters[eDef.Id()]
	eh.m.RUnlock()

	if !ok {
		return errs.New(
			errs.M("couldn't find waiter for EventDefinition"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("event_definition_id", eDef.Id()),
			errs.D("event_definition_type", eDef.Type()))
	}

	if err := w.Process(eDef); err != nil {
		return errs.New(
			errs.M("event definition processing failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("waiter_id", w.Id()),
			errs.D("event_definition_id", eDef.Id()),
			errs.D("event_definition_type", eDef.Type()),
			errs.E(err))
	}

	if len(w.EventProcessors()) == 0 {
		return eh.RemoveWaiter(w)
	}

	return nil
}

// RemoveWaiter removes single waiter from the EventHub waiter's list.
func (eh *eventHub) RemoveWaiter(w eventproc.EventWaiter) error {
	if w == nil {
		return errs.New(
			errs.M("event waiter is nil"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	eh.m.Lock()
	defer eh.m.Unlock()

	w, ok := eh.waiters[w.EventDefinition().Id()]
	if !ok {
		return errs.New(
			errs.M("waiter isn't found"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("waiter_id", w.Id()),
			errs.D("event_definition_id", w.EventDefinition().Id()),
			errs.D("event_definition_type", w.EventDefinition().Type()))
	}

	return nil
}

// ----------------------------------------------------------------------------

// interfaces check
var _ eventproc.EventProducer = (*eventHub)(nil)
