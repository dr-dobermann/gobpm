package instance

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// trackEvent is a message a track sends to the Instance event loop, which is
// the sole owner of instance lifecycle state. Tracks never mutate that state
// directly — they emit these and loop() applies them in order.
type trackEvent struct {
	node         flow.Node
	eDef         flow.EventDefinition
	track        *track
	taskID       string
	escCode      string
	compRef      string
	mergedIDs    []string
	compSnapshot []data.Data
	flows        []*flow.SequenceFlow
	msgDefIDs    []string
	condDefs     []*events.ConditionalEventDefinition
	changes      []data.Change
	compWait     bool
	kind         trackEventKind
}

// trackEventKind enumerates the track→loop event kinds.
type trackEventKind uint8

// trackEventKindNames is the kind→name table, keyed by the constant so it stays
// correct if the iota block is reordered. Keep it in sync with that block below.
var trackEventKindNames = [...]string{
	evFork:             "fork",
	evEnded:            "ended",
	evAwaiting:         "awaiting",
	evMerged:           "merged",
	evParked:           "parked",
	evFailed:           "failed",
	evWaiting:          "waiting",
	evDeliver:          "deliver",
	evMoved:            "moved",
	evBoundary:         "boundary",
	evTerminate:        "terminate",
	evTaskWaiting:      "taskWaiting",
	evJobWaiting:       "jobWaiting",
	evDataCommit:       "dataCommit",
	evScopeOpen:        "scopeOpen",
	evScopeTerminate:   "scopeTerminate",
	evCallWaiting:      "callWaiting",
	evScopeHandlerFire: "scopeHandlerFire",
	evEscalate:         "escalate",
	evCompensate:       "compensate",
	evTransactionCancel: "transactionCancel",
}

// String returns the lower-case event-kind name for logging.
func (k trackEventKind) String() string {
	if int(k) >= len(trackEventKindNames) {
		return "unknown"
	}

	return trackEventKindNames[k]
}

const (
	// evFork: build one new track per extra active outgoing flow.
	evFork trackEventKind = iota
	// evEnded: a track's run() has returned.
	evEnded
	// evAwaiting: a track reached a synchronizing join, did not complete it,
	// and its goroutine returned — it is retained as a record (AwaitingMerge).
	evAwaiting
	// evMerged: the surviving track absorbed the listed awaiting tracks at a
	// synchronizing join (flip them to Merged, fold their lineage in).
	evMerged
	// evParked: a track blocked at a reachability join (OR-join), suspending its
	// goroutine. Unlike evAwaiting, the goroutine is alive (blocked), so it is NOT
	// decremented from the active count; the loop rechecks the join and may signal
	// the track to resume (survivor) or return (merged). SRD-022.
	evParked
	// evFailed: a track's run() returned in TrackFailed (its node execution errored).
	// The loop surfaces the track's error as an instance failure (lastErr + terminate
	// via Instance.fail) instead of treating it as a plain evEnded that would let the
	// instance complete silently. FIX-008.
	evFailed
	// evWaiting: a track entered TrackWaitForEvent and parked on its evtCh. Emitted BEFORE
	// the catch node registers its hub waiters (SRD-027 FR-5) so the loop records the track as
	// parked-and-undelivered before any evDeliver can target it; the loop adds it to the
	// waiting set and indexes its msgDefIDs (Message catch defs) → track (FR-8).
	evWaiting
	// evDeliver: a producer handed a fired event (eDef) to the loop (SRD-027 FR-2). A
	// track-carried evDeliver (Signal/Timer via track.ProcessEvent) targets ev.track directly;
	// a track-less one (Message via Instance.ProcessEvent, FR-8) is resolved through the
	// msgEDef→track index and correlation-gated before the flip. The loop dispatches to the
	// track's evtCh iff it is parked-and-undelivered, else drops it (the losing arm of an
	// Event-Based gateway / a duplicate fire / a correlation mismatch — FR-4/FR-8).
	evDeliver
	// evMoved: a track advanced onto a new node (ev.node carries it). The loop sets its own
	// position view (position[track] = node) so reachability and joins read the loop-owned
	// map instead of the track's currentStep cross-goroutine (ADR-017 Rule 2, SRD-028 FR-1/FR-2).
	evMoved
	// evBoundary: an interrupting boundary event fired over its guarded activity (ev.node is the
	// boundary, ev.track the guarded host). Emitted by a boundaryWatch off the hub goroutine; the
	// loop arbitrates the completion-vs-fire race, cancels the host track, and continues on the
	// boundary's exception flow (SRD-029 FR-5/FR-8). The boundary-watch peer of evDeliver.
	evBoundary
	// evTerminate: a Terminate End Event was reached (SRD-030 FR-2). Instance.Terminate
	// emits it onto the loop's own channel — the single-writer lane every signal uses — and
	// the loop abnormally terminates the instance (stopAll). Emitted from the terminate
	// track before its own evEnded, so FIFO guarantees stopping is set first; it carries no
	// track and does NOT touch the active count (the terminate track's evEnded accounts for it).
	evTerminate
	// evTaskWaiting: a track reached a UserTask and parked it as a human task (ev.taskID is the
	// minted task id, ev.node the UserTask). The loop records it in the task registry and
	// announces it to the TaskDistributor; completion arrives later via a taskReq (ADR-020,
	// SRD-034). It is the human-task peer of evWaiting — the UserTask registers no hub waiter.
	evTaskWaiting
	// evJobWaiting: a track reached a worker-dispatched ServiceTask and parked it (ev.taskID is
	// the minted JobID, ev.node the ServiceTask). The loop binds the operation input from scope,
	// enqueues a job on the WorkerDispatcher, and records jobID → track; the worker's terminal
	// report arrives later via a jobReq and resumes the track (ADR-021, SRD-036). It is the
	// external-worker peer of evTaskWaiting — the ServiceTask registers no hub waiter.
	evJobWaiting
	// evDataCommit: a node's frame commit produced a non-empty changed-path set (ev.changes;
	// ev.node the committing node, ev.track the committing track). Emitted only when the
	// snapshot precomputed HasConditionals, so a conditional-free process never pays for it
	// (SRD-048 FR-10/NFR-1). The loop sweeps its armed conditionals: re-evaluate those whose
	// dependency statement is absent or overlaps the diff, fire on a false→true edge, apply
	// fires in arming order (ADR-006 v.3 §2.7).
	evDataCommit
	// evScopeOpen: a track parked on a composite node (an embedded Sub-Process; ev.node).
	// The loop opens the child scope, seeds the inner tracks per the validated shape, and
	// resumes the host with a synthetic completion when the scope drains (SRD-049 FR-8/9).
	// Emitted mid-run only; a born-parked composite is opened by the spawn path instead.
	evScopeOpen
	// evScopeTerminate: a Terminate End Event was reached INSIDE a sub-process (ev.track is
	// the terminating track; its scope path names the dying scope). The loop cancels only
	// that scope's tracks, closes it, and resumes the parked host — the parent continues
	// (BPMN §13.5.6, SRD-049 FR-11). A root-scope Terminate keeps evTerminate/stopAll.
	evScopeTerminate
	// evCallWaiting: a track reached a Call Activity and parked it (ev.node is the
	// CallActivity). The loop resolves the declared inputs at the caller's scope, launches
	// the child instance through the ProcessInvoker, records the call → track, and starts a
	// watcher that reports the child's completion via a callReq to resume the track (ADR-023
	// §2.7, SRD-050 FR-5/FR-6). It is the child-instance peer of evJobWaiting — a Call
	// Activity registers no hub waiter and parks for a whole child instance, not one job.
	evCallWaiting
	// evScopeHandlerFire: a scope-armed Event Sub-Process handler's trigger fired
	// (ev.node is the event-sub node, ev.eDef the fired definition). Emitted by a
	// scopeHandlerWatch off the hub goroutine (or the loop-local conditional sweep).
	// The loop cancels the enclosing scope and runs the handler in it — the
	// scope-level peer of evBoundary (ADR-023 v.2 §2.10, SRD-052 FR-5/FR-7).
	evScopeHandlerFire
	// evEscalate: an escalation throw (Escalation Intermediate Throw or End
	// Event) raised a non-critical escalation on ev.track (ev.escCode is the
	// code). Emitted from the throwing node's Exec via renv.Escalate, BEFORE the
	// throwing track's own evMoved/evEnded, so FIFO lets the loop resolve the
	// escalation first. The loop walks ev.track's scope chain to the innermost
	// matching catcher — an Escalation boundary or event-sub-process start
	// (SRD-058 FR-2). Unlike evFailed it does NOT tear down the throwing track:
	// the token continues (Intermediate Throw) or ends normally (End Event) on
	// its own; only an interrupting catcher cancels its scope. Unmatched at the
	// root, it is logged (not faulted, never silently dropped — FR-4).
	evEscalate
	// evCompensate: a Compensation throw asked for compensation of completed
	// work (ev.compRef targets one activity, "" the enclosing scope; SRD-059
	// FR-5/FR-6). Emitted by the wait-throw's park (compWait=true — ev.track
	// is parked and the loop resumes it when the sweep drains) or by
	// renv.Compensate for a fire-and-forget throw (compWait=false). The loop
	// resolves it directly against the completion ledger — reverse completion
	// order, sequential handlers; unresolved is logged, never a fault (FR-8).
	evCompensate
	// evTransactionCancel: a Cancel End Event was reached inside a Transaction
	// Sub-Process (ev.track is the aborting track). The loop aborts the enclosing
	// Transaction scope — compensate completed activities, terminate residuals,
	// exit via the Cancel boundary (BPMN §10.7, ADR-028 §2.3, SRD-061 FR-5).
	// Resolved loop-locally, never through the hub; mirrors evScopeTerminate.
	evTransactionCancel
)
