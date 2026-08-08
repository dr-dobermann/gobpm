package thresher

// SignalCatchers reports how many signal-catch processors the engine's hub
// currently holds for name — the deterministic readiness gate for black-box
// signal tests (FIX-021): a catcher's token parks before its hub registration
// runs, so neither a parked token nor a sleep proves a thrown signal has a
// catcher yet. Counts processors, not waiters — a second instance of the same
// shared-id catch joins the existing waiter. Compiled only into test binaries
// (export_test pattern); returns 0 when the hub is not the concrete eventhub
// (a mock).
func SignalCatchers(th *Thresher, name string) int {
	h, ok := th.eventHub.(interface{ SignalCatchers(string) int })
	if !ok {
		return 0
	}

	return h.SignalCatchers(name)
}

// SetCancelParkSeam installs the function Cancel runs between its state check
// and the direct cancel — the window an instance can park in. The dehydration
// harness that can produce a parked instance lives in thresher_test, and the
// window it aims at is here; this is the bridge. Pass nil to clear it.
func SetCancelParkSeam(f func()) { cancelParkSeam = f }
