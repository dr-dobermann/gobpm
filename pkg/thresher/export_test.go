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
