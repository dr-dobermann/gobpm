package instance

import (
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/model"
	"github.com/matryer/is"
)

func TestNewToken(t *testing.T) {
	is := is.New(t)

	// dummy instance
	inst := new(Instance)

	tk := newToken(model.EmptyID(), inst)
	is.True(tk != nil)
	is.True(tk.id != model.EmptyID())

	id := model.NewID()
	tk = newToken(id, inst)
	is.True(tk != nil)
	is.True(tk.id == id)

	tk = newToken(id, nil)
	is.True(tk == nil)
}

func TestSplitToken(t *testing.T) {
	is := is.New(t)

	// dummy instance
	inst := new(Instance)

	tk := newToken(model.EmptyID(), inst)
	is.True(tk != nil)

	// invalid new statused
	is.True(tk.split(1, Inactive) == nil)
	is.True(tk.split(1, Triggered) == nil)

	sameTk := tk.split(0, Alive)
	is.True(len(sameTk) == 1)
	is.True(sameTk[0] == tk)
	is.True(sameTk[0].getState() == Alive)
	is.True(tk.getState() == Alive)

	childTk := tk.split(1, Alive)
	is.True(len(childTk) == 1)
	is.True(childTk[0].getState() == Alive)
	is.True(tk.getState() == Inactive)
	is.True(len(tk.nexts) == 1 && len(childTk[0].prevs) == 1)
	is.True(tk.nexts[0] == childTk[0] && childTk[0].prevs[0] == tk)

	// invalid token state
	is.True(tk.split(1, Alive) == nil)
}

func TestTokenState(t *testing.T) {
	is := is.New(t)

	// dummy instance
	inst := new(Instance)

	tk := newToken(model.EmptyID(), inst)
	is.True(tk != nil)

	is.True(tk.updateState(WaitForTrigger))  // Alive -> WaitForTrigger
	is.True(!tk.updateState(Alive))          // WaitForTrigger !-> Alive
	is.True(tk.updateState(Triggered))       // WaitForTrigger -> Triggered
	is.True(!tk.updateState(Alive))          // Triggered !-> Alive
	is.True(!tk.updateState(Inactive))       // Triggered !-> Inactive
	is.True(!tk.updateState(WaitForTrigger)) // Triggered !-> WaitForTrigger

	// check token status updater
	chSt := make(chan tokenUpdateInfo)

	newTk := newToken(model.EmptyID(), inst)
	newTk.setStatusUpdater(chSt)
	is.True(newTk.updateState(WaitForTrigger))
	update := <-chSt
	is.True(update.oldState == Alive)
	is.True(update.newState == WaitForTrigger)
}

func TestTokenJoin(t *testing.T) {
	is := is.New(t)

	// dummy instance
	inst := new(Instance)

	newTks := newToken(model.EmptyID(), inst).split(2, Alive)
	is.True(newTks != nil)
	is.True(len(newTks) == 2)

	err := newTks[0].join(newTks[1])
	is.NoErr(err)
	tk := newTks[0]
	is.True(newTks[1].getState() == Inactive)
	is.True(len(tk.prevs) == 2)
	is.True(tk.prevs[1] == newTks[1])
	is.True(len(newTks[1].nexts) == 1)
	is.True(newTks[1].nexts[0] == tk)
	is.True(tk.state == Alive)
}

func TestTokenGroup(t *testing.T) {
	is := is.New(t)

	// dummy instance
	inst := new(Instance)

	// testing exclusive tokenGroup
	tks := newToken(model.EmptyID(), inst).split(3, WaitForTrigger)
	is.True(len(tks) == 3)

	tg := newTokenGroup(model.EmptyID(), inst, Exclusive, tks...)
	is.True(tg != nil)
	is.True(len(tg.tokens) == 3)

	chUp := make(chan tokenUpdateInfo)

	tks[1].setStatusUpdater(chUp)

	go func() {
		for sUp := range chUp {
			t.Logf("tkn #%v state changed from %s to %s",
				sUp.tokenID, sUp.oldState, sUp.newState)
		}
	}()

	is.True(tks[1].updateState(Triggered))
	is.True(tks[0].getState() == Inactive)
	is.True(tks[2].getState() == Inactive)

	// imposible state change: Inactive !-> Triggered
	is.True(!tks[2].updateState(Triggered))

	time.Sleep(time.Second)
}
