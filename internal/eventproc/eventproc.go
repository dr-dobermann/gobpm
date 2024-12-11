package eventproc

import (
	"context"
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// ============================================================================
//
//	EventProcessor
//
// ============================================================================
// EventProcessor handles single event.
type EventProcessor interface {
	foundation.Identifyer

	// ProcessEvent processes single event definition by node, it registered
	// in EventProducer.
	ProcessEvent(context.Context, flow.EventDefinition) error
}

// ============================================================================
//
//	EventProcessor
//
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
	UnregisterEvent(ep EventProcessor, eDefId string) error

	// PropagateEvent gets a eventDefinitions and sends it to all
	// EventProcessors registered for this type of EventDefinition.
	PropagateEvent(context.Context, flow.EventDefinition) error
}

// ============================================================================
//
//	EventProcessor
//
// ============================================================================
// EventWaiter gets on startup an eventDefinition and EventProcessor
// expected the event defined.
// Then it controls single event defined by eventDefinition and
// once event fired, send appropriata eventDefinition with actual data to
// the EventProcessor.
type EventWaiter interface {
	foundation.Identifyer

	// EventDefinition returns an event definition the eventWaiter is
	// waiting for.
	EventDefinition() flow.EventDefinition

	// EventProcessor returns the EventProcessor expecting the registered
	// EventDefinition.
	EventProcessor() EventProcessor

	// Service runs the waiting/handling routine of registered event defined.
	Service(ctx context.Context) error

	// Stop terminates waiting cycle of the waiter.
	Stop() error

	// State returns current state of the EventWaiter.
	State() EventWaiterState
}

type EventWaiterState int

const (
	WSCreated EventWaiterState = iota
	WSReady
	WSRunned
	WSEnded
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
		"Failed",
	}[ws]
}
