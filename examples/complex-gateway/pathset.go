package main

import (
	"fmt"
	"sync"
)

// pathSet records which branch tasks actually ran, so the example can ASSERT the
// branch the gateway chose. An exclusive gateway that routed the wrong way still
// completes the process, so "it finished" proves nothing about routing.
//
// Recorded from inside the tasks themselves rather than from an engine observer:
// observer facts are delivered ASYNCHRONOUSLY, so a check right after
// WaitCompletion can run before the facts arrive — which made an earlier version
// of this assertion fail about 1 run in 7. A task that records as it executes is
// synchronous with the run by construction.
type pathSet struct {
	ran map[string]bool
	m   sync.Mutex
}

func newPathSet() *pathSet {
	return &pathSet{ran: map[string]bool{}}
}

// mark records that the named task executed.
func (p *pathSet) mark(name string) {
	p.m.Lock()
	defer p.m.Unlock()

	p.ran[name] = true
}

// check reports an error unless every task in taken ran and none in skipped did
// — the two halves of "the right branch was chosen".
func (p *pathSet) check(taken, skipped []string) error {
	p.m.Lock()
	defer p.m.Unlock()

	for _, n := range taken {
		if !p.ran[n] {
			return fmt.Errorf("task %q never ran", n)
		}
	}

	for _, n := range skipped {
		if p.ran[n] {
			return fmt.Errorf("task %q ran but its branch was not taken", n)
		}
	}

	return nil
}

// ranCount reports how many of the named tasks ran — for a join that fires on
// the Nth of M branches, where WHICH ones arrive first is not fixed.
func (p *pathSet) ranCount(names ...string) int {
	p.m.Lock()
	defer p.m.Unlock()

	n := 0

	for _, name := range names {
		if p.ran[name] {
			n++
		}
	}

	return n
}
