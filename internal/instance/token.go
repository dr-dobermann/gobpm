package instance

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

type TokenState uint8

const (
	TokenAlive TokenState = iota
	TokenWaitForEvent
	TokenDead
)

func (ts TokenState) Validate() error {
	if ts < TokenAlive || ts > TokenDead {
		return errs.New(
			errs.M("invalid token state: %d", ts),
			errs.C(errorClass, errs.InvalidParameter))
	}

	return nil
}

type token struct {
	foundation.ID

	inst  *Instance
	state TokenState
	prevs []*token
	nexts []*token
}

// updateState sets new valid state of the token
func (t *token) updateState(newState TokenState) {
	if err := newState.Validate(); err != nil {
		errs.Panic(err)
	}

	t.state = newState
}
