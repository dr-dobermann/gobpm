package instance

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dr-dobermann/gobpm/model"
)

type TokenHandler interface {
	TakeToken(t *Token) error
	ReturnTokens() ([]*Token, error)
}

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

type Token struct {
	id    model.Id
	inst  *Instance
	state tokenState
	prevs []*Token
	nexts []*Token

	group *tokenGroup

	updCh chan tokenUpdateInfo
	ctx   context.Context
}

// creates a new token for the instance.
func newToken(tID model.Id, inst *Instance) *Token {
	if inst == nil {
		return nil
	}

	if tID == model.EmptyID() {
		tID = model.NewID()
	}

	return &Token{id: tID, inst: inst}
}

func (t *Token) setStatusUpdater(uCh chan tokenUpdateInfo) {

	st := t.getState()

	if t.updCh == nil && uCh != nil &&
		st != Inactive && st != Triggered {
		t.updCh = uCh
	}
}

func (t *Token) getState() tokenState {
	if t.group != nil {
		t.group.Lock()
		defer t.group.Unlock()

		return t.state
	}

	return t.state
}

func (t *Token) sendStateUpdate(newState tokenState) {
	if t.updCh != nil {
		oldState := t.state
		go func() {
			t.updCh <- tokenUpdateInfo{t.id, oldState, newState}
			if newState == Triggered || newState == Inactive {
				defer close(t.updCh)
				t.updCh = nil
			}
		}()
	}
}

func (t *Token) updateState(newState tokenState) bool {
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
func (t *Token) split(n int, newState tokenState) []*Token {
	if t.getState() == Inactive ||
		newState == Triggered ||
		newState == Inactive {

		return nil
	}

	tt := []*Token{}

	if n <= 0 {
		return append(tt, t)
	}

	for i := 0; i < n; i++ {
		nt := &Token{
			id:    model.NewID(),
			inst:  t.inst,
			state: newState,
			prevs: append([]*Token{}, t)}

		tt = append(tt, nt)

		t.nexts = append(t.nexts, nt)
		t.updateState(Inactive)
	}

	return tt
}

// func (t *Token) GetPrevious() []*Token {
// 	tt := make([]*Token, len(t.prevs))

// 	copy(tt, t.prevs)

// 	return tt
// }

// joins one token to another.
func (t *Token) join(jt *Token) error {
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
	tokens []*Token
	inst   *Instance
}

func newTokenGroup(
	id model.Id,
	inst *Instance,
	gType groupType,
	tt ...*Token) *tokenGroup {

	if inst == nil {
		return nil
	}

	if id == model.EmptyID() {
		id = model.NewID()
	}

	tg := &tokenGroup{
		id:     id,
		gType:  gType,
		tokens: []*Token{},
		inst:   inst,
	}

	if len(tt) > 0 {
		tg.addTokens(tt...)
	}

	return tg
}

func (tg *tokenGroup) addTokens(tt ...*Token) int {
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

func (tg *tokenGroup) updateState(t *Token, newState tokenState) bool {
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
