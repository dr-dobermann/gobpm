package instance

import (
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
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
	staging *values.Array[any]
	// kind names the iteration shape for the record (SRD-090.A FR-6). It
	// is derived from the NODE rather than posted, because every shape
	// that reaches a mirror is decided by the node's own loop
	// characteristics — a decorator posting it would be telling the loop
	// something the loop can already read.
	kind string
	// instances describe the activity's executor set (FR-6), posted by the
	// decorator (scopeIterPost). It is the decorator's to report for every
	// fanned-out shape: a leaf opens no scope and spawns no track, so
	// nothing else can see its instances at all, and a composite fan-out is
	// invisible the same way in the window before its first scope opens.
	// Empty for a SERIAL composite, whose one live pass is its open scope
	// and whose position rides the drain protocol.
	instances    []checkpoint.IterationInstance
	n            int
	completed    int
	conditionMet bool
}

// iterKindOf names the iteration shape a node drives, for the record
// (SRD-090.A FR-6). The empty string for a node that iterates not at all —
// which never reaches a mirror, since only an own-iteration host has one.
func iterKindOf(node flow.Node) string {
	if standardLoopOf(node) != nil {
		return iterKindStdLoop
	}

	mi := multiInstanceOf(node)
	if mi == nil {
		return ""
	}

	if mi.IsSequential() {
		return iterKindMISequential
	}

	return iterKindMIParallel
}

// The iteration shapes a record names. They describe the SHAPE, not the
// node: a sequential Multi-Instance reads the same whether its instances
// are executions of a leaf or child scopes of a composite, which is the
// point of recording instances rather than tracks.
const (
	iterKindStdLoop      = "std_loop"
	iterKindMISequential = "mi_sequential"
	iterKindMIParallel   = "mi_parallel"
)

// ensureIterMirror registers (or returns) the mirror for a host whose
// activity drives its own iteration. Runs on the loop goroutine, from
// handleScopeOpen — the runner is parked awaiting the reply, so the
// miState reads are fenced by the request channel.
func (ls *loopState) ensureIterMirror(
	host *track, node flow.Node,
) *iterMirror {
	if m, ok := ls.iter[host.ID()]; ok {
		return m
	}

	m := &iterMirror{kind: iterKindOf(node)}
	if st := host.miState; st != nil {
		m.n = st.numberOfInstances
		m.staging = st.staging
	}

	ls.iter[host.ID()] = m

	return m
}

// markIterDrain records one completed serial pass (SRD-082 FR-2). Runs
// on the loop goroutine from completeScope, before the runner resumes.
//
// A FANNED-OUT instance is not a serial pass and is skipped: the host's
// loopCounter stands still for the whole fan-out, so it cannot say how many
// instances are done, and the decorator posts that set itself
// (postPosition). Deriving it here would overwrite the truth with a zero
// (SRD-090.A M3b).
func (ls *loopState) markIterDrain(entry *scopeEntry) {
	if entry.group != nil || entry.instance ||
		!drivesOwnIteration(entry.node) {
		return
	}

	if m, ok := ls.iter[entry.host.ID()]; ok {
		m.completed = entry.host.loopCounterSnap() + 1
	}
}
