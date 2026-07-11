package thresher

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// observerBuffer is the per-observer event-channel depth. A slower observer
// than this many buffered events drops the excess (Subscription.Dropped) rather
// than blocking the engine (ADR-013 §2.2; the buffer size is an SRD-018 choice).
const observerBuffer = 64

// EventKind classifies an observation event. It is an OPEN vocabulary: a host
// must tolerate unknown values, since kinds are added additively (ADR-013 §2.4).
// It is a type alias of observability.Kind — the canonical engine-event
// vocabulary (ADR-013 v.2 §2.6) — so a delivered public Event and an internal
// observable event share one set of kind values with no conversion.
type EventKind = observability.Kind

const (
	// EventInstanceState is an instance lifecycle transition.
	EventInstanceState EventKind = observability.KindInstanceState

	// EventNodeProgress is a token reaching a node in a given state.
	EventNodeProgress EventKind = observability.KindNodeProgress

	// EventTokenMoved is reserved for a future distinct token-movement event.
	EventTokenMoved EventKind = "TokenMoved"
)

// The engine-wide kinds (ADR-013 v.2 §2.6) are delivered on the engine-scope
// stream (Thresher.Observe): the transition name travels in Event.State and the
// identifiers in Event.Details. Each is the same value as its observability.Kind
// (EventKind is a type alias), so a delivered public event and an internal
// observable event share one vocabulary.
const (
	EventEngineState      EventKind = observability.KindEngineState
	EventHubState         EventKind = observability.KindHubState
	EventProcessLifecycle EventKind = observability.KindProcessLifecycle
	EventGatewayDecision  EventKind = observability.KindGatewayDecision
	EventFlow             EventKind = observability.KindEventFlow
	EventCorrelation      EventKind = observability.KindCorrelation
	EventJobState         EventKind = observability.KindJobState
	EventTaskState        EventKind = observability.KindTaskState
	EventBoundary         EventKind = observability.KindBoundary
	EventFault            EventKind = observability.KindFault
	EventDataChange       EventKind = observability.KindDataChange
)

// Event is one observation event delivered to an Observer. It carries identity,
// state and timing only — never process payloads (the masking rule).
type Event struct {
	At         time.Time
	Details    map[string]string
	Kind       EventKind
	InstanceID string
	NodeID     string
	NodeName   string
	State      string
}

// Observer receives observation events. OnEvent is called from a per-observer
// drain goroutine, never on the engine's execution path; it MAY block without
// stalling the engine (the engine drops events past the buffer instead), and a
// panic in it is recovered.
type Observer interface {
	OnEvent(Event)
}

// Subscription is a live observer registration on an instance's event stream.
type Subscription struct {
	cancel  func()
	dropped *atomic.Uint64
}

// Dropped reports how many events were dropped because the observer fell behind
// the buffer (SRD-018 FR-9). Best-effort, monotonic.
func (s *Subscription) Dropped() uint64 {
	return s.dropped.Load()
}

// Cancel deregisters the observer and drains any buffered events, then stops the
// drain goroutine. Idempotent.
func (s *Subscription) Cancel() {
	s.cancel()
}

// Observe registers o on the instance's lifecycle/token/node event stream
// (SRD-018, ADR-013 §2.2). Delivery is best-effort and lossy: events are
// buffered per observer and drained by one goroutine; if the observer is slower
// than the buffer, the excess is dropped (Subscription.Dropped) and the engine
// never blocks. A panicking OnEvent is recovered. Cancel the returned
// Subscription to stop observing.
func (h *InstanceHandle) Observe(o Observer) *Subscription {
	id := h.inst.ID()
	ch := make(chan Event, observerBuffer)
	done := make(chan struct{})

	var dropped atomic.Uint64

	go func() {
		defer close(done)

		for ev := range ch {
			deliver(o, ev)
		}
	}()

	// The instance-scope visibility filter (SRD-041 FR-8 / ADR-013 §2.11): the
	// policy is per-recipient with no scope carve-out, so it gates handle
	// observers too. Asserted once here at registration; absent ⇒ pass-through.
	filter, _ := h.inst.AuthorizationProvider().(observability.ObservationFilter)

	cancelReg := h.inst.AddObserver(func(ie observability.ObsEvent) {
		if filter != nil {
			filtered, ok := filter.FilterObservation(o, ie)
			if !ok {
				return // policy-denied — not a counted drop
			}

			ie = filtered
		}

		ev := toPublicEvent(ie)
		ev.InstanceID = id

		select {
		case ch <- ev:
		default:
			dropped.Add(1)
		}
	})

	var once sync.Once

	return &Subscription{
		dropped: &dropped,
		cancel: func() {
			once.Do(func() {
				// Deregister first: AddObserver's cancel takes the instance's
				// observer write-lock, which fences any in-flight fan-out, so no
				// sink send is in progress once it returns — making close(ch)
				// safe (no send-on-closed-channel). Then drain to completion.
				cancelReg()
				close(ch)
				<-done
			})
		},
	}
}

// deliver calls the observer, containing any panic so one bad observer cannot
// crash the drain goroutine or affect others (ADR-013 §5).
func deliver(o Observer, ev Event) {
	defer func() { _ = recover() }()

	o.OnEvent(ev)
}

// toPublicEvent projects the canonical observable event onto the delivered
// public Event (SRD-041 §3.2). Kind passes through (EventKind is a type alias of
// observability.Kind); the phase becomes State (unchanged strings for the v.1
// kinds — T-10); instance_id is promoted from details to the InstanceID field
// for v.1 compatibility while the full details map rides along. Shared by the
// instance handle and the engine-scope producer so both scopes deliver one shape.
func toPublicEvent(ev observability.ObsEvent) Event {
	return Event{
		At:         ev.At,
		Details:    ev.Details,
		Kind:       ev.Kind,
		InstanceID: ev.Details[observability.AttrInstanceID],
		NodeID:     ev.NodeID,
		NodeName:   ev.NodeName,
		State:      string(ev.Phase),
	}
}
