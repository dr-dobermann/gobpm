package exec

import (
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// WaitHolders is the engine-level DURABLE holder registry a dehydratable wait
// registers with at ARM time (ADR-007 v.2 §2.4, SRD-071 FR-3): the trigger
// source that outlives the instance's goroutines and wakes it on fire. It is
// implemented by the engine (the thresher) and consumed by the instance loop —
// a released instance never receives a trigger at its own (vanished) loop; the
// holder does, and it drives the wake.
//
// An Instance carries a nil WaitHolders for a library embedder without a
// thresher, or when checkpointing is off: every wait then stays resident (no
// wait releases without a holder that can wake it — the ADR §2.4 safety). M3
// covers the timer kind; the message/signal and human-task holders (M4/M6)
// extend this seam.
type WaitHolders interface {
	// HoldTimer registers a timer wait's ABSOLUTE deadline with the engine
	// timer service, keyed to (instanceID, trackID). At the deadline the
	// service wakes the instance, passing eDef as the trigger that fires the
	// woken timer node through. cycles carries a repeating timer's remaining
	// count. An error means the hold was NOT taken — the caller must keep the
	// wait resident (fall back to the in-hub waiter) rather than lose the timer.
	HoldTimer(
		instanceID, trackID string,
		eDef flow.EventDefinition,
		deadline time.Time,
		cycles int,
	) error

	// ReleaseTimer withdraws a held timer, keyed to (instanceID, trackID): the
	// wait completed on wake, the instance terminated, or a sibling arm won an
	// Event-Based Gateway race. Idempotent — a withdraw of an unknown hold is a
	// no-op.
	ReleaseTimer(instanceID, trackID string)
}
