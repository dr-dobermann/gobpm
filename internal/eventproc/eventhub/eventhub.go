/*
Package eventhub provides event hub implementation for BPMN processes.

This package is part of GoBPM - Business Process Management Engine for Go.
See LICENSE file for license information.

Author: dr-dobermann (rgabitov@gmail.com)
Repository: https://github.com/dr-dobermann/gobpm
*/
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
	// EventHub processes all registration requests from EventProcessors
	// for specific eventDefinition.
	// On every pair EventProcessor - eventDefinition EventHub creates
	// personal eventWaiter and runs its Service in separate go-routine.
	EventHub struct {
		ctx     context.Context
		waiters map[string]eventproc.EventWaiter
		events  []flow.EventDefinition
		m       sync.RWMutex
		started bool
	}
)

// New creates a new eventHandler.
func New() (*EventHub, error) {
	return &EventHub{
			waiters: map[string]eventproc.EventWaiter{},
			events:  []flow.EventDefinition{},
		},
		nil
}

// Run starts main cycle of eventHub.
func (eh *EventHub) Run(ctx context.Context) error {
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
func (eh *EventHub) RegisterEvent(
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
	w, ok := eh.waiters[eDef.ID()]
	eh.m.RUnlock()
	if ok {
		if err := w.AddEventProcessor(ep); err != nil {
			return errs.New(
				errs.M("couldn't add event processor to waiter"),
				errs.C(errorClass, errs.OperationFailed),
				errs.D("waiter_id", w.ID()),
				errs.D("event_definition_id", eDef.ID()),
				errs.D("event_definition_type", eDef.Type()),
				errs.D("event_processor_id", ep.ID()))
		}

		return nil
	}

	w, err := waiters.CreateWaiter(eh, ep, eDef)
	if err != nil {
		return errs.New(
			errs.M("eventWaiter building failed"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.D("event_processor_id", ep.ID()),
			errs.D("event_definition_id", eDef.ID()),
			errs.E(err))
	}

	eh.m.Lock()
	eh.waiters[eDef.ID()] = w
	eh.m.Unlock()

	if err := w.Service(eh.ctx); err != nil {
		return errs.New(
			errs.M("failed to start waiter service"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("waiter_id", w.ID()),
			errs.E(err))
	}

	return nil
}

// UnregisterEvent removes the registered eventDefintions for single
// EventProcessor.
func (eh *EventHub) UnregisterEvent(
	ep eventproc.EventProcessor,
	eDefID string,
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
	w, ok := eh.waiters[eDefID]
	eh.m.RUnlock()

	if !ok {
		return errs.New(
			errs.M("couldn't find waiter for the event definition"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("event_definition_id", eDefID))
	}

	if err := w.RemoveEventProcessor(ep); err != nil {
		return errs.New(
			errs.M("couldn't remove event processor from waiter"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("event_waiter_id", w.ID()),
			errs.D("event_processor_id", ep.ID()),
			errs.D("event_definition_id", eDefID),
			errs.E(err))
	}

	if len(w.EventProcessors()) == 0 {
		if w.State() == eventproc.WSRunned {
			if err := w.Stop(); err != nil {
				return errs.New(
					errs.M("waiter stop failed"),
					errs.C(errorClass, errs.OperationFailed),
					errs.D("waiter_id", w.ID()),
					errs.D("event_definition_idf", w.EventDefinition().ID()),
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
func (eh *EventHub) PropagateEvent(
	_ context.Context,
	eDef flow.EventDefinition,
) error {
	if !eh.started {
		return errs.New(
			errs.M("eventHub isn't started"),
			errs.C(errorClass, errs.InvalidState))
	}

	eh.m.RLock()
	w, ok := eh.waiters[eDef.ID()]
	eh.m.RUnlock()

	if !ok {
		return errs.New(
			errs.M("couldn't find waiter for EventDefinition"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("event_definition_id", eDef.ID()),
			errs.D("event_definition_type", eDef.Type()))
	}

	if err := w.Process(eDef); err != nil {
		return errs.New(
			errs.M("event definition processing failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("waiter_id", w.ID()),
			errs.D("event_definition_id", eDef.ID()),
			errs.D("event_definition_type", eDef.Type()),
			errs.E(err))
	}

	if len(w.EventProcessors()) == 0 {
		return eh.RemoveWaiter(w)
	}

	return nil
}

// RemoveWaiter removes single waiter from the EventHub waiter's list.
func (eh *EventHub) RemoveWaiter(w eventproc.EventWaiter) error {
	if w == nil {
		return errs.New(
			errs.M("event waiter is nil"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	eh.m.Lock()
	defer eh.m.Unlock()

	w, ok := eh.waiters[w.EventDefinition().ID()]
	if !ok {
		return errs.New(
			errs.M("waiter isn't found"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("waiter_id", w.ID()),
			errs.D("event_definition_id", w.EventDefinition().ID()),
			errs.D("event_definition_type", w.EventDefinition().Type()))
	}

	return nil
}

// ----------------------------------------------------------------------------

// interfaces check
var _ eventproc.EventProducer = (*EventHub)(nil)
