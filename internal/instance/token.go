package instance

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

type TokenState uint8

const (
	TokenInvalid TokenState = iota
	TokenAlive
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

func newToken(inst *Instance) *token {
	if inst == nil {
		errs.Panic("empty instance on token creation")

		return nil
	}

	return &token{
		ID:    *foundation.NewID(),
		inst:  inst,
		state: TokenAlive,
		prevs: []*token{},
		nexts: []*token{},
	}
}

// updateState sets new valid state of the token
func (t *token) updateState(newState TokenState) {
	if err := newState.Validate(); err != nil {
		errs.Panic(err)

		return
	}

	t.state = newState
}

// split creates a new splitCount tokens from the t token.
func (t *token) split(splitCount int) []*token {
	tt := make([]*token, 0, splitCount)

	for i := 0; i < splitCount; i++ {
		tt[i] = newToken(t.inst)
		tt[i].prevs = append(t.prevs, t)
		t.nexts = append(t.nexts, tt[i])
	}

	return tt
}
