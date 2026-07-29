package exec

import (
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// WaitKind names WHAT a held wait belongs to. The set is closed by the BPMN
// object model — a trigger either belongs to the wait a token is parked on, or
// to a boundary event guarding the activity it sits in — so this is a named
// constant rather than a boolean flag: `WaitBoundary` says what it means at
// the call site, `true` does not, and no invalid combination can be expressed.
//
// It matters because the two wake DIFFERENTLY. A node wait's trigger fires
// through the parked node itself, so the wake carries it as a pending trigger
// and the woken track continues from there. A boundary's trigger belongs to
// the boundary, not to the guarded node: the wake is trigger-ABSENT, the
// restored boundary re-arms at its recorded deadline, and the fire that
// follows is a fork at the boundary event with the guarded track as its parent
// — interrupting cancels that parent, non-interrupting leaves it running —
// which is precisely what the loop's fireBoundary produces.
type WaitKind uint8

const (
	// WaitNode is a wait the token is parked on. The zero value: an
	// unannotated hold behaves as it always did.
	WaitNode WaitKind = iota
	// WaitBoundary is a boundary event guarding the activity the token sits
	// on (SRD-071 FR-9a).
	WaitBoundary
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
	// kind says whether the deadline belongs to the parked node or to a
	// boundary guarding it, which decides how the wake delivers it (WaitKind).
	HoldTimer(
		instanceID, trackID string,
		eDef flow.EventDefinition,
		deadline time.Time,
		cycles int,
		kind WaitKind,
	) error

	// HoldSubscription registers a message/signal wait's hub subscription
	// against the ENGINE rather than the instance, keyed to
	// (instanceID, trackID): the subscription outlives the instance's
	// goroutines, so a trigger arriving at a released instance reaches the
	// holder and wakes it.
	//
	// convKeys are the instance's conversation key VALUES at arm time — what
	// the instance's own registration would have contributed, so the holder
	// subscribes to exactly the same conversation (ADR-016). A foreign
	// conversation is therefore filtered by the broker and never wakes the
	// instance; an empty slice subscribes wildcard, as an un-keyed wait does.
	//
	// An error means the hold was NOT taken and the caller must keep the wait
	// resident (registering its own subscription) rather than lose the trigger.
	HoldSubscription(
		instanceID, trackID string,
		eDef flow.EventDefinition,
		convKeys []string,
		kind WaitKind,
	) error

	// HoldTask registers a parked human task against the ENGINE, keyed to
	// (instanceID, trackID). Unlike a timer or a subscription there is nothing
	// to subscribe: the task already lives in the distributor's inbox
	// independent of the instance's residency (ADR-020). The hold only records
	// WHICH track the task belongs to, so a Take/Complete on it can wake a
	// released instance. An error means the hold was NOT taken and the caller
	// must keep the wait resident.
	HoldTask(instanceID, trackID, taskID string) error

	// ReleaseWaits withdraws EVERY hold taken for a track — its deadline, its
	// subscriptions, or the whole set an Event-Based Gateway armed. Called when
	// the wait fires, when the instance tears down, and (the EBG case) when one
	// arm wins and its siblings must be released. Idempotent: withdrawing
	// unknown holds is a no-op.
	ReleaseWaits(instanceID, trackID string)
}
