// Package eventproc provides event processing interfaces and implementations.
package eventproc

import (
	"context"
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// EventProcessor represents an event processor interface.
// ============================================================================
// EventProcessor handles single event.
type EventProcessor interface {
	foundation.Identifyer

	// ProcessEvent processes single event definition by node, it registered
	// in EventProducer.
	ProcessEvent(context.Context, flow.EventDefinition) error
}

// EventProducer represents an event producer interface.
// ============================================================================
// EventProducer registers events with event processors which expect those
// events.
type EventProducer interface {
	// RegisterEvent registers the EventDefinition the EventProcessor
	// is waiting for. Once the EventProducer got the event with event
	// definition matched with registered event definition Id, it calls the
	// registered EventProcessor with given event definition.
	RegisterEvent(EventProcessor, flow.EventDefinition) error

	// UnregisterEvents removes event definition to EventProcessor link from
	// EventProducer.
	UnregisterEvent(ep EventProcessor, eDefID string) error

	// PropagateEvent sends a fired throw event's eventDefinition
	// up to chain of EventProducers
	PropagateEvent(context.Context, flow.EventDefinition) error
}

// EventHub represents a central event distribution hub.
// ============================================================================
// EventHub runs events processing engine and managing pack of EventWaiters.
type EventHub interface {
	EventProducer

	// Run starts event processing.
	Run(context.Context) error

	// RemoveWaiter removes single waiter from the EventHub waiter's list.
	RemoveWaiter(EventWaiter) error
}

// EventWaiter represents an event waiter interface.
// ============================================================================
// EventWaiter gets on startup an eventDefinition and EventProcessor
// expected the event defined.
// Then it controls single event defined by eventDefinition and
// once event fired, send appropriata eventDefinition with actual data to
// the EventProcessor.
//
// Once eventDefinition is processed by all registered EventProcessors, the
// waiter unregister itself from eventHub and stop its working,
type EventWaiter interface {
	foundation.Identifyer

	// EventDefinition returns an event definition the eventWaiter is
	// waiting for.
	EventDefinition() flow.EventDefinition

	// AddEventProcessor adds single EventProcessor into waiter's list of
	// EventProcessors, waiting for the EventDefinition.
	// If the EventProcessor already exists in waiters queue, no errors returned.
	AddEventProcessor(EventProcessor) error

	// RemoveEventProcessor removes the ep EventProcessor from waiter's event
	// processors list.
	RemoveEventProcessor(EventProcessor) error

	// EventProcessors returns the EventProcessor expecting the registered
	// EventDefinition.
	EventProcessors() []EventProcessor

	// Process processed single event given by EventHub through EventPropagate
	// call.
	Process(flow.EventDefinition) error

	// Service runs the waiting/handling routine of registered event defined.
	Service(ctx context.Context) error

	// Stop terminates waiting cycle of the waiter.
	Stop() error

	// State returns current state of the EventWaiter.
	State() EventWaiterState
}

// EventWaiterState represents the state of an event waiter.
type EventWaiterState int

const (
	// WSCreated represents a created event waiter state.
	WSCreated EventWaiterState = iota
	// WSReady represents a ready event waiter state.
	WSReady
	// WSRunned represents a running event waiter state.
	WSRunned
	// WSEnded represents an ended event waiter state.
	WSEnded
	// WSStopped represents a stopped event waiter state.
	WSStopped
	// WSFailed represents a failed event waiter state.
	WSFailed
)

// String implements Stringer interface for EventWaiterState.
// If there is an invalid value of the state it panics.
func (ws EventWaiterState) String() string {
	if ws < WSCreated || ws > WSFailed {
		errs.Panic("undefined EventWaiterState: " + strconv.Itoa(int(ws)))

		return ""
	}

	return []string{
		"Created",
		"Ready",
		"Runned",
		"Ended",
		"Stopped",
		"Failed",
	}[ws]
}
