package main

import (
	"fmt"
	"strings"
	"sync"
)

// runLog records the ORDER in which the booking and undo steps executed. A set
// would not be enough here: the claim this example demonstrates is that
// compensation runs the handlers in REVERSE order of the activities that
// completed, so "both undo handlers ran" is true even of a wrong implementation
// that ran them forwards.
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
