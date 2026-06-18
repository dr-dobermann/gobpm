package instance

import "time"

// ObsKind classifies a host-observable event (SRD-018, ADR-013 §2.2).
type ObsKind uint8

const (
	// ObsInstanceState is an instance lifecycle transition (Active, Completed,
	// Terminating, Terminated).
	ObsInstanceState ObsKind = iota

	// ObsNodeProgress is a track reaching a node in a token state — the
	// token-movement / node-progress signal.
	ObsNodeProgress
)

// String returns the kind name.
func (k ObsKind) String() string {
	switch k {
	case ObsInstanceState:
		return "InstanceState"

	case ObsNodeProgress:
		return "NodeProgress"

	default:
		return "Unknown"
	}
}

// ObsEvent is a host-observable lifecycle/token/node event (SRD-018). It carries
// identity and state only — never payloads (the masking rule, ADR-010/011).
type ObsEvent struct {
	At       time.Time
	NodeID   string
	NodeName string
	State    string
	Kind     ObsKind
}

// obsReg is one registered observer sink with its cancellation id.
type obsReg struct {
	fn func(ObsEvent)
	id uint64
}

// AddObserver registers a sink on the instance's observation stream and returns
// a cancel func that deregisters it (SRD-018). The sink MUST NOT block: it is
// called on the execution hot path (every instance/track transition) under a
// read lock, so the public thresher handle wraps it with a buffered, lossy,
// separately-drained delivery. A nil sink is ignored.
func (inst *Instance) AddObserver(fn func(ObsEvent)) func() {
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
// it fences any in-flight notify (which holds the read lock for the whole
// fan-out): once it returns, the removed sink is never called again — letting
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

// notify fans an event out to every registered sink under the read lock. The
// sinks are non-blocking, so the lock is held only briefly; holding it across
// the fan-out is what lets removeObserver guarantee no sink call is in flight.
// With no observers it returns immediately — the event (and its timestamp) is
// built only when someone is listening, keeping the execution hot path free.
func (inst *Instance) notify(kind ObsKind, nodeID, nodeName, state string) {
	inst.obsMu.RLock()
	defer inst.obsMu.RUnlock()

	if len(inst.observers) == 0 {
		return
	}

	ev := ObsEvent{
		At:       inst.now(),
		NodeID:   nodeID,
		NodeName: nodeName,
		State:    state,
		Kind:     kind,
	}

	for _, r := range inst.observers {
		r.fn(ev)
	}
}
