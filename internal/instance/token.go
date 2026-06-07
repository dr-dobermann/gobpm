package instance

import (
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// TokenState represents the current state of a token in the process
// instance execution flow.
type TokenState uint8

// Token state constants define the possible states of a token during
// process execution.
const (
	// TokenInvalid represents an invalid token state
	TokenInvalid TokenState = iota
	// TokenAlive represents an active token ready for processing
	TokenAlive
	// TokenWaitForEvent represents a token waiting for an event
	TokenWaitForEvent
	// TokenConsumed represents a token that has been consumed
	TokenConsumed
	// TokenWithdrawn represents a token withdrawn at an Event-Based Gateway
	// race loss. The value exists for the projection; its producer arrives
	// with the Event-Based Gateway (gateway SRD).
	TokenWithdrawn
)

// Validate checks if the TokenState is valid.
func (ts TokenState) Validate() error {
	if ts < TokenAlive || ts > TokenWithdrawn {
		return errs.New(
			errs.M("invalid token state: %d", ts),
			errs.C(errorClass, errs.InvalidParameter))
	}

	return nil
}

// The token is no longer a stored object — it exists only as the derived
// projection types below, produced by track.Token() / Instance.GetTokens() /
// Instance.TokenHistory(). TokenState above is the projected value's type.

// Token is the logical, derived view of a track's current control-flow
// position — the BPMN "token" as a projection, not a stored object.
type Token struct {
	Node  flow.Node
	State TokenState
}

// StepVisit is one entry of a token's path history with its timing.
type StepVisit struct {
	Node  flow.Node
	At    time.Time
	State TokenState
}

// TokenPath is the recorded path of one token (one track), with lineage.
type TokenPath struct {
	TrackID  string
	ParentID string // immediate parent track (fork origin); "" if root
	Steps    []StepVisit
	Terminal TokenState
}

// stepUpdate is one recorded track-state transition: the node the track was
// at, the track state it entered, and when. The token state is projected
// from it; the node + time give the path and its timing.
type stepUpdate struct {
	node  flow.Node
	at    time.Time
	state trackState
}

// tokenStateFor projects a track state onto the BPMN token state.
func tokenStateFor(ts trackState) TokenState {
	switch ts {
	case TrackReady, TrackExecutingStep, TrackProcessStepResults:
		return TokenAlive

	case TrackWaitForEvent:
		return TokenWaitForEvent

	case TrackEnded, TrackMerged, TrackCanceled, TrackFailed:
		// Canceled maps to Consumed here; the Withdrawn case
		// (Event-Based Gateway race loss) is wired with that gateway
		// (gateway SRD).
		return TokenConsumed

	default:
		return TokenInvalid
	}
}
