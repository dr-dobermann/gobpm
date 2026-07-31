package main

import (
	"fmt"
	"sync"
)

// seen captures the value the caller actually read back from the child, so the
// example can ASSERT the data contract rather than only print it. The child
// computing the wrong total — or the output mapping never landing at all —
// would still complete both processes and exit 0.
//
// Captured inside the task rather than through an engine observer: observer
// facts are delivered asynchronously and can still be in flight when
// WaitCompletion returns.
type seen struct {
	total any
	got   bool
	m     sync.Mutex
}

func newSeen() *seen {
	return &seen{}
}

// record stores the total the show task read from the caller's scope.
func (s *seen) record(total any) {
	s.m.Lock()
	defer s.m.Unlock()

	s.total, s.got = total, true
}

// check reports an error unless the caller read exactly want.
func (s *seen) check(want int) error {
	s.m.Lock()
	defer s.m.Unlock()

	if !s.got {
		return fmt.Errorf("the caller never read %q back from the child",
			"total")
	}

	if s.total != want {
		return fmt.Errorf("caller read total=%v, want %d", s.total, want)
	}

	return nil
}
