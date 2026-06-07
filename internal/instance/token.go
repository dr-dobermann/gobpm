package instance

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
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

// The token is no longer a stored object — it exists only as a derived
// projection (see track.Token() / Instance.GetTokens() / tokenStateFor in
// track.go). TokenState above is the projected value's type.
