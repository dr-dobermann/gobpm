package main

import (
	"fmt"
	"strings"
	"sync"
)

// runLog records the loopCounter value the engine published on each pass. A
// set would not be enough: the claim is that a post-tested Standard Loop runs
// at 0, 1 and 2 and then stops, so both the COUNT and the SEQUENCE matter. A
// loop that ran the right number of times with a stuck counter, or one that
// ran an extra pass, would print plausible lines and complete the same way.
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

// check reports an error unless the steps executed in exactly this order.
func (r *runLog) check(want ...string) error {
	r.m.Lock()
	defer r.m.Unlock()

	if strings.Join(r.seq, ",") != strings.Join(want, ",") {
		return fmt.Errorf("ran %v, want %v", r.seq, want)
	}

	return nil
}
