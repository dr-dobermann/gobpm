package main

import (
	"sync"
	"time"

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

// missing returns the wanted phases that never arrived, waiting up to timeout
// for them.
//
// The wait is not padding: observer facts are delivered ASYNCHRONOUSLY, so they
// can still be in flight when WaitCompletion has already returned. Checking
// immediately made this assertion fail about 1 run in 40. Anything asserted from
// the fact stream has to allow for that; anything recorded inside a task does
// not, because that is synchronous with the run.
func (w *lifecycleWatch) missing(
	timeout time.Duration, want ...observability.Phase,
) []observability.Phase {
	deadline := time.Now().Add(timeout)

	for {
		absent := w.absentNow(want)
		if len(absent) == 0 || time.Now().After(deadline) {
			return absent
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// absentNow reports which wanted phases have not been seen yet.
func (w *lifecycleWatch) absentNow(
	want []observability.Phase,
) []observability.Phase {
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
