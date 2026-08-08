package instance

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// iterMirror is the loop-owned mirror of an off-loop iteration
// decorator's position (SRD-082 FR-2): the checkpoint capture runs on
// the loop, and the runner's own state (miState, its loop locals) is
// goroutine-owned — so the loop records what the decorator protocol
// already shows it. The runner stays authoritative at runtime; the
// mirror exists for capture only.
//
// n and staging are copied once at the first scopeOpen — the runner is
// parked in its roundtrip then, so the read is fenced; staging is
// loop-written thereafter (captureSequentialOutput), which makes the
// capture's read loop-serialized. completed advances on each serial
// drain (completeScope, before the runner is resumed — the same fence
// scopeLoopCounter relies on). conditionMet arrives over the protocol
// (scopeNote) when the runner's completionCondition fires.
type iterMirror struct {
	staging      *values.Array[any]
	n            int
	completed    int
	conditionMet bool
}

// ensureIterMirror registers (or returns) the mirror for a host whose
// composite drives its own iteration. Runs on the loop goroutine, from
// handleScopeOpen — the runner is parked awaiting the reply, so the
// miState reads are fenced by the request channel.
func (ls *loopState) ensureIterMirror(host *track) *iterMirror {
	if m, ok := ls.iter[host.ID()]; ok {
		return m
	}

	m := &iterMirror{}
	if st := host.miState; st != nil {
		m.n = st.numberOfInstances
		m.staging = st.staging
	}

	ls.iter[host.ID()] = m

	return m
}

// markIterDrain records one completed serial pass (SRD-082 FR-2). Runs
// on the loop goroutine from completeScope, before the runner resumes.
func (ls *loopState) markIterDrain(entry *scopeEntry) {
	if entry.group != nil || !drivesOwnIteration(entry.node) {
		return
	}

	if m, ok := ls.iter[entry.host.ID()]; ok {
		m.completed = entry.host.loopCounterSnap() + 1
	}
}
