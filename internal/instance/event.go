package instance

import "github.com/dr-dobermann/gobpm/pkg/model/flow"

// trackEvent is a message a track sends to the Instance event loop, which is
// the sole owner of instance lifecycle state. Tracks never mutate that state
// directly — they emit these and loop() applies them in order.
type trackEvent struct {
	// track is the subject: the forking parent for evFork, the ended
	// track for evEnded.
	track *track
	// flows, for evFork, are the extra outgoing flows (beyond the one the
	// parent continues on) that the loop builds a new track for.
	flows []*flow.SequenceFlow
	kind  trackEventKind
}

// trackEventKind enumerates the track→loop event kinds.
type trackEventKind uint8

const (
	// evFork: build one new track per extra active outgoing flow.
	evFork trackEventKind = iota
	// evEnded: a track's run() has returned.
	evEnded
)
