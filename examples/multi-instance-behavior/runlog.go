package main

import (
	"fmt"
	"sync"
)

// runLog records the ORDER in which the votes and the quorum notification
// happened. A set is not enough: the claim is that the notification fires only
// once the quorum is MET, so what matters is how many votes precede it. A
// notification thrown on the first vote would print the same line and complete
// the same way.
//
// Recorded from inside the tasks rather than from an engine observer: observer
// facts are delivered asynchronously and can still be in flight when
// WaitCompletion returns, whereas a task that records as it executes is
// synchronous with the run by construction.
type runLog struct {
	seq []string
	m   sync.Mutex
}

func newRunLog() *runLog {
	return &runLog{}
}

// mark appends the named step to the execution order.
func (r *runLog) mark(name string) {
	r.m.Lock()
	defer r.m.Unlock()

	r.seq = append(r.seq, name)
}

// count reports how many times name was recorded.
func (r *runLog) count(name string) int {
	r.m.Lock()
	defer r.m.Unlock()

	n := 0

	for _, s := range r.seq {
		if s == name {
			n++
		}
	}

	return n
}

// precede reports an error unless at least n occurrences of before were
// recorded ahead of the first occurrence of name.
func (r *runLog) precede(name, before string, n int) error {
	r.m.Lock()
	defer r.m.Unlock()

	seen := 0

	for _, s := range r.seq {
		switch s {
		case before:
			seen++

		case name:
			if seen < n {
				return fmt.Errorf("ran %v: %q fired after %d %q, want at "+
					"least %d", r.seq, name, seen, before, n)
			}

			return nil
		}
	}

	return fmt.Errorf("ran %v, but %q never happened", r.seq, name)
}
