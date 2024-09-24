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
	TokenWaitForInteraction
	TokenConsumed
)

func (ts TokenState) Validate() error {
	if ts < TokenAlive || ts > TokenConsumed {
		return errs.New(
			errs.M("invalid token state: %d", ts),
			errs.C(errorClass, errs.InvalidParameter))
	}

	return nil
}

type token struct {
	foundation.ID

	inst  *Instance
	trk   *track
	state TokenState
	prevs []*token
	nexts []*token
}

// newToken creates a new token and adds it to the Instance.
func newToken(inst *Instance, trk *track) *token {
	if inst == nil {
		errs.Panic("empty instance on token creation")

		return nil
	}

	t := token{
		ID:    *foundation.NewID(),
		inst:  inst,
		trk:   trk,
		state: TokenAlive,
		prevs: []*token{},
		nexts: []*token{},
	}

	inst.addToken(&t)

	return &t
}

// updateState sets new valid state of the token
func (t *token) updateState(newState TokenState) error {
	if err := newState.Validate(); err != nil {
		return err
	}

	t.state = newState

	if t.state == TokenConsumed {
		t.inst.tokenConsumed(t)
	}

	return nil
}

// split creates a new splitCount tokens from the t token.
// the first token is the token t
func (t *token) split(splitCount int) []*token {
	tt := make([]*token, 0, splitCount)

	tt = append(tt, t)

	for i := 1; i < splitCount; i++ {
		tt[i] = newToken(t.inst, t.trk)
		tt[i].prevs = t.prevs
		tt[i].prevs = append(tt[i].prevs, t)
		t.nexts = append(t.nexts, tt[i])
	}

	return tt
}
