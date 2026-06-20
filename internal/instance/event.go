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
	// flows, for evFork, are the extra outgoing flows (beyond the one the
	// parent continues on) that the loop builds a new track for.
	flows []*flow.SequenceFlow
	// mergedIDs, for evMerged, are the ids of the previously-awaiting tracks the
	// surviving track absorbed at a synchronizing join. Ids, not pointers: the
	// loop resolves them against inst.tracks (which it owns) to flip their state.
	mergedIDs []string
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
)
