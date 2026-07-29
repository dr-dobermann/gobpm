package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// residency watches the two facts that bracket a dehydration cycle and prints
// them as they happen. They are the operator's view of the whole feature:
// Dehydrated says WHAT the instance is waiting on and that it now holds zero
// goroutines; Hydrated says what woke it and whether the wake continued the
// flow or finished the instance.
type residency struct {
	mu    sync.Mutex
	seen  map[observability.Phase]int
	quiet bool
}

func newResidency() *residency {
	return &residency{seen: map[observability.Phase]int{}}
}

func (r *residency) OnFact(f observability.Fact) {
	if f.Kind != observability.KindInstanceState {
		return
	}

	switch f.Phase {
	case observability.PhaseDehydrated:
		r.count(f.Phase)

		if !r.quiet {
			fmt.Printf("      · dehydrated   waits=%s×%s goroutines=%s\n",
				f.Details["wait_kinds"], f.Details["waits"],
				f.Details["goroutines"])
		}

	case observability.PhaseHydrated:
		r.count(f.Phase)

		if !r.quiet {
			fmt.Printf("      · hydrated     trigger=%s outcome=%s\n",
				f.Details["trigger"], f.Details["outcome"])
		}
	}
}

func (r *residency) count(p observability.Phase) {
	r.mu.Lock()
	r.seen[p]++
	r.mu.Unlock()
}

// dehydrations reports how many instances have released so far.
func (r *residency) dehydrations() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.seen[observability.PhaseDehydrated]
}

// awaitDehydration waits until one more instance has released, or gives up.
// Returns whether it happened — a case that is SUPPOSED to stay resident calls
// this and expects false.
func (r *residency) awaitDehydration(from int, within time.Duration) bool {
	deadline := time.Now().Add(within)

	for time.Now().Before(deadline) {
		if r.dehydrations() > from {
			return true
		}

		time.Sleep(5 * time.Millisecond)
	}

	return false
}

// await reads one "the wait fired" note, or reports the flow never got there.
func await(done <-chan string, within time.Duration) (string, bool) {
	select {
	case note := <-done:
		return note, true

	case <-time.After(within):
		return "", false
	}
}
