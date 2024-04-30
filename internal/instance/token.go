package instance

import (
	"strconv"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

type TokenState uint8

const (
	TokenInvalid TokenState = iota
	TokenAlive
	TokenWaitForEvent
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

	m sync.Mutex

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
func (t *token) updateState(newState TokenState) error {
	if err := newState.Validate(); err != nil {
		return err
	}

	t.m.Lock()
	defer t.m.Unlock()

	t.state = newState

	return nil
}

// split creates a new splitCount tokens from the t token.
// the first token is the token t
func (t *token) split(splitCount int) []*token {
	if splitCount < 1 {
		errs.Panic("invalid number of split tokens [" +
			strconv.Itoa(splitCount) + "]")
	}

	tt := make([]*token, 0, splitCount)

	tt = append(tt, t)

	for i := 1; i < splitCount; i++ {
		tt[i] = newToken(t.inst)
		tt[i].prevs = t.prevs
		tt[i].prevs = append(tt[i].prevs, t)
		t.nexts = append(t.nexts, tt[i])
	}

	return tt
}
