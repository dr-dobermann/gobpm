package instance

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dr-dobermann/gobpm/model"
)

type tokenState uint16

const (
	Alive tokenState = iota
	WaitForTrigger
	Triggered
	Inactive
)

func (ts tokenState) String() string {
	return []string{
		"Alive",
		"WaitForTrigger",
		"Triggered",
		"Inactive",
	}[ts]
}

type tokenUpdateInfo struct {
	tokenID  model.Id
	oldState tokenState
	newState tokenState
}

type token struct {
	id    model.Id
	inst  *Instance
	state tokenState
	prevs []*token
	nexts []*token

	group *tokenGroup

	updCh chan tokenUpdateInfo
	ctx   context.Context
}

// creates a new token for the instance.
func newToken(tID model.Id, inst *Instance) *token {
	if inst == nil {
		return nil
	}

	if tID == model.EmptyID() {
		tID = model.NewID()
	}

	return &token{id: tID, inst: inst}
}

func (t *token) setStatusUpdater(
	ctx context.Context,
	uCh chan tokenUpdateInfo) {

	if t.updCh == nil && uCh != nil && ctx != nil {
		t.updCh = uCh
		t.ctx = ctx
	}
}

func (t *token) getState() tokenState {
	if t.group != nil {
		t.group.Lock()
		defer t.group.Unlock()

		return t.state
	}

	return t.state
}

func (t *token) sendStateUpdate(newState tokenState) {
	if t.updCh != nil {
		oldState := t.state
		go func() {
			select {
			case <-t.ctx.Done():
				close(t.updCh)

			case t.updCh <- tokenUpdateInfo{t.id, oldState, newState}:
			}
		}()
	}
}

func (t *token) updateState(newState tokenState) bool {
	if t.state == newState {
		return true
	}

	if t.group != nil {
		return t.group.updateState(t, newState)
	}

	if t.state == Inactive ||
		t.state == Triggered ||
		(t.state == Alive && newState == Triggered) ||
		t.state == WaitForTrigger && newState == Alive {

		return false
	}

	t.sendStateUpdate(newState)
	t.state = newState

	return true
}

// splits token onto n new token(s) and makes splitted token inactive.
//
// if n == 0 the token itself will be returned with current state
// if n == 1 the new child of the splitted token will be created
//
// token with inactive status couldn't be splitted and nil-slice returned.
//
// if splitted token sits in tokenGroup, all grouped tokens' states could be
// changed.
func (t *token) split(n uint16, newState tokenState) []*token {
	if t.getState() == Inactive ||
		newState == Triggered ||
		newState == Inactive {

		return nil
	}

	tt := []*token{}

	if n == 0 {
		return append(tt, t)
	}

	for i := 0; i < int(n); i++ {
		nt := &token{
			id:    model.NewID(),
			inst:  t.inst,
			state: newState,
			prevs: append([]*token{}, t)}

		tt = append(tt, nt)

		t.nexts = append(t.nexts, nt)
		t.updateState(Inactive)
	}

	return tt
}

// func (t *token) GetPrevious() []*token {
// 	tt := make([]*token, len(t.prevs))

// 	copy(tt, t.prevs)

// 	return tt
// }

// joins one token to another.
func (t *token) join(jt *token) error {
	if t.getState() == Inactive {
		return fmt.Errorf("couldn't join to inactive token %v", t.id)
	}

	if jt == nil {
		return errors.New("nil-token couldn't be joined")
	}

	if jt.getState() == Inactive {
		return fmt.Errorf("couldn't join an inactive token %v", jt.id)
	}

	jt.nexts = append(t.nexts, t)
	t.prevs = append(t.prevs, jt)
	jt.updateState(Inactive)

	return nil
}

type groupType uint8

const (
	Exclusive groupType = iota
	Parallel
)

type tokenGroup struct {
	sync.Mutex

	id     model.Id
	gType  groupType
	tokens []*token
	inst   *Instance
}

func newTokenGroup(
	id model.Id,
	inst *Instance,
	gType groupType,
	tt ...*token) *tokenGroup {

	if inst == nil {
		return nil
	}

	if id == model.EmptyID() {
		id = model.NewID()
	}

	tg := &tokenGroup{
		id:     id,
		gType:  gType,
		tokens: []*token{},
		inst:   inst,
	}

	if len(tt) > 0 {
		tg.addTokens(tt...)
	}

	return tg
}

func (tg *tokenGroup) addTokens(tt ...*token) int {
	added := 0

	tg.Lock()
	defer tg.Unlock()

	for _, t := range tt {
		if t == nil ||
			t.group != nil ||
			t.state != WaitForTrigger ||
			t.inst != tg.inst {

			continue
		}

		tg.tokens = append(tg.tokens, t)

		t.group = tg

		added++
	}

	return added
}

func (tg *tokenGroup) updateState(t *token, newState tokenState) bool {
	if tg != t.group {
		return false
	}

	tg.Lock()
	defer tg.Unlock()

	tSt := t.state
	if tSt == Triggered || tSt == Inactive {
		return false
	}

	if tSt == Alive && newState == Triggered ||
		tSt == WaitForTrigger && (newState == Inactive || newState == Alive) {

		return false
	}

	if tg.gType == Exclusive &&
		tSt == WaitForTrigger &&
		newState == Triggered {

		for _, gt := range tg.tokens {
			if gt != t {
				gt.sendStateUpdate(Inactive)

				gt.state = Inactive
			}
		}
	}

	t.sendStateUpdate(newState)

	t.state = newState

	return true
}
