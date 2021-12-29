package model

import (
	"testing"

	"github.com/google/uuid"
	"github.com/matryer/is"
)

func TestNewToken(t *testing.T) {
	tID := NewID()
	pID := NewID()
	tok := NewToken(tID, pID)
	if tok == nil {
		t.Error("Couldn't create a token")
	}

	if tok != nil && tok.ID() != tID {
		t.Errorf("Invalid token ID: wanted %v, got %v", tID, tok.ID())
	}

	tok2 := NewToken(Id(uuid.Nil), Id(uuid.Nil))
	if tok2 != nil {
		t.Error("Token created with an empty process Id")
	}
}

func TestSplitJoinToken(t *testing.T) {
	// splitting tests
	tok := NewToken(NewID(), NewID())

	// Splitting on 0 tokens returns the token
	// itself
	tt := tok.Split(0)
	if len(tt) != 1 {
		t.Errorf("Couldn't copy token. Got %d token(s)", len(tt))
	}

	if tt[0].ID() != tok.ID() {
		t.Errorf("Invalid copy of token. Got %v, want %v",
			tt[0].ID(), tok.ID())
	}

	tt = tok.Split(3)
	if len(tt) != 3 {
		t.Errorf("Expecting 3 new token, got %d", len(tt))
	}

	if tok.State() == TSLive {
		t.Error("Invalid splitting. Previous token has Live state")
	}

	// will panic on splitting inactive token
	// tok.Split(3)

	for _, tk := range tt {
		// status forked tokens
		if tk.State() != TSLive {
			t.Error("Created by splitting token has Inactive status")
		}

		// check reverse chain links presence
		if len(tk.prevs) == 0 {
			t.Error("Invalid splitted token. Has no previous token")
		}

		// check reverse chain links value
		if tk.prevs[0].ID() != tok.ID() {
			t.Error("Splitting error no previous token in the new one")
		}

		// check forward links in original token
		i := -1
		for ti, t := range tok.nexts {
			if t.id == tk.id {
				i = ti
				break
			}
		}

		if i == -1 {
			t.Error("Forward chain in split broken " + tok.id.String())
		}
	}

	// join tests
	tt[0].Join(tt[1])

	// check state of joined token
	if tt[1].State() == TSLive {
		t.Error("Invalid token state after joining. Still Live")
	}

	// check reverse chain links in result token
	if len(tt[0].prevs) != 2 {
		t.Error("Invalid tokens joining. Chain information is lost")
	}

	if tt[0].prevs[0] != tok || tt[0].prevs[1] != tt[1] {
		t.Error("Invalid token joining. Chain information is incomplete")
	}

	// check forvar chain link in joined token
	if len(tt[1].nexts) != 1 || tt[1].nexts[0] != tt[0] {
		t.Error("Invalid token joining. Forward chain links is broken")
	}

	tt[0].Join(tt[1])

	if len(tt[0].prevs) > 2 {
		t.Error("Iactive token joining")
	}

	// should fire panic due to joining to Inactive token
	// tt[1].Join(tt[2])
}

func TestId(t *testing.T) {
	is := is.New(t)

	id := NewID()

	s := id.String()

	is.True(s[len(s)-4:] == id.GetLast(4))
}
