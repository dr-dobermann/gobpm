package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// seen captures what the stamp task read inside the run, so the example can
// ASSERT the contract rather than only print it: a child computing the wrong
// total, or an output that never landed, would still complete and exit 0.
type seen struct {
	total any
	got   bool
	m     sync.Mutex
}

func newSeen() *seen {
	return &seen{}
}

// record stores the total the stamp task read from the quote's scope.
func (s *seen) record(total any) {
	s.m.Lock()
	defer s.m.Unlock()

	s.total, s.got = total, true
}

// check reports an error unless the stamp task read exactly want.
func (s *seen) check(want int) error {
	s.m.Lock()
	defer s.m.Unlock()

	if !s.got {
		return fmt.Errorf("the stamp task never read %q", "total")
	}

	if s.total != want {
		return fmt.Errorf("stamp read total=%v, want %d", s.total, want)
	}

	return nil
}

// checkOutputs asserts the collected result: the required "total" carries
// want and the optional "started_at" was produced and is a real time.
func checkOutputs(outs []data.Data, want int) error {
	byName := make(map[string]any, len(outs))
	for _, d := range outs {
		byName[d.Name()] = d.Value().Get(context.Background())
	}

	if byName["total"] != want {
		return fmt.Errorf("outputs carry total=%v, want %d", byName["total"],
			want)
	}

	if at, ok := byName["started_at"].(time.Time); !ok || at.IsZero() {
		return fmt.Errorf("outputs carry started_at=%v, want a time",
			byName["started_at"])
	}

	return nil
}
