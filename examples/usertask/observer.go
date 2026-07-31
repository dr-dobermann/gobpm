package main

import (
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// lifecycleWatch records which ownership phases the task actually went through,
// so the example can ASSERT the flow it claims to demonstrate rather than only
// print it. A run where the driver silently failed to claim — or where the task
// completed without ever being announced — still exits 0 otherwise.
//
// Facts arrive on the engine's goroutines, so the set is mutex-guarded.
type lifecycleWatch struct {
	seen map[observability.Phase]bool
	m    sync.Mutex
}

func newLifecycleWatch() *lifecycleWatch {
	return &lifecycleWatch{seen: map[observability.Phase]bool{}}
}

// OnFact records every user-task phase the engine reports.
func (w *lifecycleWatch) OnFact(f observability.Fact) {
	if f.Kind != observability.KindTaskState {
		return
	}

	w.m.Lock()
	defer w.m.Unlock()

	w.seen[f.Phase] = true
}

// missing returns the wanted phases that never arrived.
func (w *lifecycleWatch) missing(want ...observability.Phase) []observability.Phase {
	w.m.Lock()
	defer w.m.Unlock()

	var absent []observability.Phase

	for _, p := range want {
		if !w.seen[p] {
			absent = append(absent, p)
		}
	}

	return absent
}
