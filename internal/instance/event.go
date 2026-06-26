package instance

import "github.com/dr-dobermann/gobpm/pkg/model/flow"

// trackEvent is a message a track sends to the Instance event loop, which is
// the sole owner of instance lifecycle state. Tracks never mutate that state
// directly — they emit these and loop() applies them in order.
type trackEvent struct {
	// track is the subject: the forking parent for evFork, the ended track for
	// evEnded, the awaiting track for evAwaiting, the surviving (completing)
	// track for evMerged.
	track *track
	// node carries a node for two kinds: for evMoved the node the track just advanced onto
	// (the loop sets position), and for evParked the join node the track suspended on (the
	// loop sets parked). Carried in the event so the loop never infers it from a
	// cross-goroutine read of the track's currentStep (ADR-017 Rule 2, SRD-028 FR-2/FR-3).
	node flow.Node
	// eDef, for evDeliver, is the fired event definition the loop dispatches to the
	// subject track's evtCh (SRD-027 FR-2). For a Message evDeliver (track == nil,
	// emitted by Instance.ProcessEvent) the loop resolves the target track from its
	// id via the msgEDef→track index (FR-8).
	eDef flow.EventDefinition
	// flows, for evFork, are the extra outgoing flows (beyond the one the
	// parent continues on) that the loop builds a new track for.
	flows []*flow.SequenceFlow
	// mergedIDs, for evMerged, are the ids of the previously-awaiting tracks the
	// surviving track absorbed at a synchronizing join. Ids, not pointers: the
	// loop resolves them against inst.tracks (which it owns) to flip their state.
	mergedIDs []string
	// msgDefIDs, for evWaiting, are the ids of the track's Message catch definitions.
	// The loop enters them into its msgEDef→track index so a fired message resolves
	// back to this track (SRD-027 FR-5/FR-8). Empty for a Signal/Timer-only wait.
	msgDefIDs []string
	kind      trackEventKind
}

// trackEventKind enumerates the track→loop event kinds.
type trackEventKind uint8

// String returns the lower-case event-kind name for logging.
func (k trackEventKind) String() string {
	switch k {
	case evFork:
		return "fork"

	case evEnded:
		return "ended"

	case evAwaiting:
		return "awaiting"

	case evMerged:
		return "merged"

	case evParked:
		return "parked"

	case evFailed:
		return "failed"

	case evWaiting:
		return "waiting"

	case evDeliver:
		return "deliver"

	case evMoved:
		return "moved"

	default:
		return "unknown"
	}
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
)
