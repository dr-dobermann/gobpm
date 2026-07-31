package main

import (
	"fmt"
	"sync"
)

// runLog records the ORDER in which the ad-hoc activities executed. A set is
// not enough here: the Router decides what runs and when, and the claim this
// example demonstrates is that close-incident runs LAST — only once the work
// it depends on has settled. A run that closed the incident first would still
// execute all four activities and complete the container.
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

// first reports an error unless name was the first activity to run.
func (r *runLog) first(name string) error {
	r.m.Lock()
	defer r.m.Unlock()

	if len(r.seq) == 0 || r.seq[0] != name {
		return fmt.Errorf("ran %v, want %q first", r.seq, name)
	}

	return nil
}

// last reports an error unless name ran once, ran after every one of others,
// and each of those ran too.
func (r *runLog) last(name string, others ...string) error {
	r.m.Lock()
	defer r.m.Unlock()

	at := func(n string) int {
		for i, s := range r.seq {
			if s == n {
				return i
			}
		}

		return -1
	}

	end := at(name)
	if end != len(r.seq)-1 {
		return fmt.Errorf("ran %v, want %q last", r.seq, name)
	}

	for _, o := range others {
		if at(o) < 0 {
			return fmt.Errorf("ran %v, but %q never ran", r.seq, o)
		}
	}

	return nil
}
