package instance

import "github.com/dr-dobermann/gobpm/pkg/observability"

// obsReg is one registered observer sink with its cancellation id.
type obsReg struct {
	fn func(observability.Fact)
	id uint64
}

// AddObserver registers a sink on the instance's observation stream and returns
// a cancel func that deregisters it (SRD-018). The sink receives the canonical
// observability.Fact — one event type from emitter to delivery (SRD-041
// FR-1). It MUST NOT block: it is called on the execution hot path (every
// instance/track transition) under a read lock, so the public thresher handle
// wraps it with a buffered, lossy, separately-drained delivery. A nil sink is
// ignored.
func (inst *Instance) AddObserver(fn func(observability.Fact)) func() {
	if fn == nil {
		return func() {}
	}

	inst.obsMu.Lock()
	defer inst.obsMu.Unlock()

	inst.obsID++
	id := inst.obsID
	inst.observers = append(inst.observers, obsReg{fn: fn, id: id})

	return func() { inst.removeObserver(id) }
}

// removeObserver drops the sink registered under id. It takes the write lock, so
// it fences any in-flight fan-out (which holds the read lock for the whole
// dispatch): once it returns, the removed sink is never called again — letting
// the handle safely tear down its buffered channel.
func (inst *Instance) removeObserver(id uint64) {
	inst.obsMu.Lock()
	defer inst.obsMu.Unlock()

	for i, r := range inst.observers {
		if r.id == id {
			inst.observers = append(inst.observers[:i], inst.observers[i+1:]...)

			return
		}
	}
}

// observe is the single emission point for an instance-scope observable event
// (SRD-041 FR-4). It stamps the event's timestamp and instance_id, fans it out
// to the instance's local handle observers (the v.1 path — built only when
// someone listens, preserving the notify hot-path guard, NFR-1), and forwards it
// to the engine sink, which writes the operator-log echo and fans out to the
// engine-scope observers. The engine fan-out/echo runs off the obsMu lock, so a
// slow producer never stalls the local dispatch.
func (inst *Instance) report(ev observability.Fact) {
	inst.obsMu.RLock()
	hasLocal := len(inst.observers) > 0
	inst.obsMu.RUnlock()

	// The engine sink is reached through the embedded runtime; a bare Instance
	// (constructed without New — the isolated unit tests) has none.
	var sink observability.Reporter
	if inst.EngineRuntime != nil {
		sink = inst.Reporter()
	}

	// The hot-path guard (NFR-1): with no local observer AND no engine sink,
	// nobody listens and nothing echoes, so skip building the event entirely —
	// and never touch the (possibly absent) runtime.
	if !hasLocal && sink == nil {
		return
	}

	// Stamp the time from the injected clock when one is set. A focused unit test
	// may build a partial Instance (a runtime but no clock) and still exercise an
	// emission path; At then stays zero — optional metadata, not a panic.
	if ev.At.IsZero() && inst.now != nil {
		ev.At = inst.now()
	}

	if ev.Details == nil {
		ev.Details = map[string]string{}
	}

	if _, ok := ev.Details[observability.AttrInstanceID]; !ok {
		ev.Details[observability.AttrInstanceID] = inst.ID()
	}

	// Call linkage (SRD-050 FR-4): a child instance stamps its caller's ids on
	// every fact so the trace stitches across the reuse boundary. Empty for a
	// top-level instance — the stamp then adds nothing.
	if inst.parentInstanceID != "" {
		ev.Details[observability.AttrParentInstanceID] = inst.parentInstanceID
		ev.Details[observability.AttrCallActivityNodeID] = inst.callNodeID
	}

	if hasLocal {
		inst.fanoutLocal(ev)
	}

	if sink != nil {
		sink.Report(ev)
	}
}

// fanoutLocal dispatches ev to the instance's local observers under the read
// lock. Holding the lock across the dispatch is what lets removeObserver
// guarantee no sink call is in flight. With no observers it returns at once — the
// local path costs only the empty check when nobody is listening.
func (inst *Instance) fanoutLocal(ev observability.Fact) {
	inst.obsMu.RLock()
	defer inst.obsMu.RUnlock()

	for _, r := range inst.observers {
		r.fn(ev)
	}
}
