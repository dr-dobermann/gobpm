// Package eventproc provides event processing interfaces and implementations.
package eventproc

import (
	"context"
	"errors"
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	pubevent "github.com/dr-dobermann/gobpm/pkg/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// ErrRejected is returned by an EventProcessor that declines a fired event that
// isn't for it — a correlation mismatch (SRD-017 §4.5 / BPMN §8.4.2: a message
// whose already-initialized key differs does not route to this conversation). A
// single-shot message waiter treats it specially: it stays subscribed and keeps
// waiting (the message is dropped) instead of terminating.
var ErrRejected = errors.New("event rejected: not for this processor")

// EventProcessor and EventProducer are the public node-facing event contracts
// (ADR-012 v.1). They live in pkg/eventproc; the aliases here let the internal
// EventHub/EventWaiter and the internal consumers keep referring to them
// unqualified while the implementations stay in this package.
type (
	// EventProcessor handles a single fired event (pkg/eventproc.EventProcessor).
	EventProcessor = pubevent.EventProcessor
	// EventProducer registers processors and propagates events
	// (pkg/eventproc.EventProducer).
	EventProducer = pubevent.EventProducer
)

// EventHub represents a central event distribution hub.
// ============================================================================
// EventHub runs events processing engine and managing pack of EventWaiters.
type EventHub interface {
	EventProducer

	// Start performs synchronous initialization of the EventHub: stores the
	// context that the hub will run under, marks the hub as started, and
	// returns. Start MUST be called exactly once before Run; calling Start
	// twice returns an "already started" error.
	//
	// The synchronous nature of Start is load-bearing: it ensures that
	// EventHub state (started flag, ctx) is visible to any subsequent caller
	// of RegisterEvent / UnregisterEvent / PropagateEvent without requiring
	// callers to synchronize via timing hacks or atomic primitives. See
	// FIX-001 for the race that motivated splitting Start from Run.
	Start(context.Context) error

	// Run is the blocking event-processing loop. It MUST be called only
	// after Start. Run returns when its context is canceled.
	//
	// Typical usage is to call Start synchronously, then invoke Run from a
	// background goroutine:
	//
	//	if err := hub.Start(ctx); err != nil { return err }
	//	go func() { _ = hub.Run(ctx) }()
	Run(context.Context) error

	// RemoveWaiter removes the waiter registered for the given event
	// definition ID from the EventHub waiter's list.
	//
	// The method takes the eventDefinition ID rather than the waiter
	// itself so callers (including a waiter calling cleanup on itself)
	// do not pass their own receiver across the interface boundary.
	// Passing a mutex-bearing receiver to a mock implementation whose
	// argument-matcher uses reflect.DeepEqual / fmt-via-reflect races
	// with concurrent calls that acquire that mutex — the race
	// detector flags the reflect read against the lock CAS.
	RemoveWaiter(eDefID string) error

	// WaiterFired is called by a waiter (from its own goroutine) to report it
	// has fired. The EventHub — the SOLE owner of waiter removal (ADR-006 v.1
	// §2.5) — removes the waiter iff it has reached a terminal state, and keeps
	// a still-running one (a persistent message waiter, or a timer mid-cycle).
	// No waiter ever removes itself: it sets its own state and reports here.
	// Takes the eventDefinition ID (not the waiter) for the same reflect-race
	// reason as RemoveWaiter.
	WaiterFired(eDefID string) error

	// RegisterPersistentEvent registers a persistent (never single-shot)
	// subscription for an event-triggered instance-starter (SRD-015): the
	// waiter fires for every matching message and is retained until it is
	// unregistered (UnregisterEvent) or stopped, rather than being removed
	// after the first fire like the single-shot in-instance receiver
	// RegisterEvent builds. Only message triggers are accepted.
	RegisterPersistentEvent(ep EventProcessor, eDef flow.EventDefinition) error
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
