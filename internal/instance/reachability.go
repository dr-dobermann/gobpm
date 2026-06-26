package instance

import (
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// CheckFlows implements exec.FlowChecker (ADR-005 v.2 §2.10, SRD-022 §6.3): given
// a synchronizing join and a set of its un-marked incoming flows, it returns the
// subset still reachable — those with a live token somewhere on a backward path
// from the flow's source to the start. Reachability is structural
// (condition-ignoring, so every edge counts) and cycle-guarded; it is computed on
// demand with no cached graph. The instance loop is the sole caller, so the live
// position set is read without contention.
func (inst *Instance) CheckFlows(
	node flow.Node,
	flows []*flow.SequenceFlow,
) ([]*flow.SequenceFlow, error) {
	return checkFlowsWith(node, flows, inst.occupiedNodes())
}

// fixedFlowChecker is an exec.FlowChecker bound to a precomputed occupied-node set, so a join
// recheck evaluates reachability against the SAME token-position snapshot it used for its
// in-transit guard. This closes the recheck double-read race (SRD-027 FIX): sampling positions
// twice let a token slipping from a branch (reachable) to the join (arrived-pending) be invisible
// to both reads, yielding a spurious unsatisfiable-rule abort (and the symmetric missed abort).
type fixedFlowChecker struct {
	occupied map[string]bool
}

// CheckFlows reuses the bound snapshot instead of re-reading live token positions.
func (f fixedFlowChecker) CheckFlows(
	node flow.Node,
	flows []*flow.SequenceFlow,
) ([]*flow.SequenceFlow, error) {
	return checkFlowsWith(node, flows, f.occupied)
}

// checkFlowsWith returns the subset of flows still reachable from a live token, given a fixed
// occupied-node snapshot. Shared by the live CheckFlows and the snapshot-bound fixedFlowChecker.
func checkFlowsWith(
	node flow.Node,
	flows []*flow.SequenceFlow,
	occupied map[string]bool,
) ([]*flow.SequenceFlow, error) {
	joinID := node.ID()

	reachable := make([]*flow.SequenceFlow, 0, len(flows))

	for _, f := range flows {
		if f == nil {
			continue
		}

		if src := f.Source(); src != nil && reachesOccupied(src, joinID, occupied) {
			reachable = append(reachable, f)
		}
	}

	return reachable, nil
}

// occupiedNodes is the set of node ids that currently hold a live token. It walks
// the loop-owned tracks by their actual position (currentStep), not the projected
// history — a freshly forked track holds a position before it has recorded any
// history, and must still count as a reacher. A track parked at a synchronizing
// join is included (it may resume and reach downstream); a dead track (Merged /
// Ended / Canceled / Failed) is excluded. Called only from loop().
func (inst *Instance) occupiedNodes() map[string]bool {
	occupied, _ := inst.joinPositions(nil)

	return occupied
}

// joinPositions takes ONE consistent snapshot of live token positions for a join recheck:
// the occupied-node set used for reachability AND whether a token is imminently arriving at
// joinNode (sitting on the join but not yet parked in TrackAwaitSync). Reading both from a
// single pass — each track's position read exactly once — is what makes the recheck race-free:
// a token slipping from a branch (reachable) to the join (arrived-pending) is seen by BOTH the
// in-transit guard and reachability as the same position, never invisible to both at once (the
// double-read race behind the spurious "activation rule unsatisfiable" abort). joinNode may be
// nil (plain occupied-set query); then inTransit is always false. Called only from loop().
func (inst *Instance) joinPositions(
	joinNode flow.Node,
) (occupied map[string]bool, inTransit bool) {
	occupied = map[string]bool{}

	for _, t := range inst.tracks {
		if t.inState(TrackMerged, TrackEnded, TrackCanceled, TrackFailed) {
			continue
		}

		pos := t.currentStep().node.ID()
		occupied[pos] = true

		// A token on the join node that has not yet parked (TrackAwaitSync) is between
		// checkFlows moving its position and synchronize recording its arrival — an
		// imminent arrival the caller must wait for, not decide around.
		if joinNode != nil && pos == joinNode.ID() && !t.inState(TrackAwaitSync) {
			inTransit = true
		}
	}

	return occupied, inTransit
}

// reachesOccupied reports whether any node on a backward path from start (walking
// Incoming → Source toward the process start) currently holds a live token. The
// join node (stopID) is never traversed — a path "through the join" does not count
// — which also bounds the walk; the visited set additionally guards cycles so a
// cyclic model cannot hang it.
func reachesOccupied(start flow.Node, stopID string, occupied map[string]bool) bool {
	visited := map[string]bool{stopID: true}
	stack := []flow.Node{start}

	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		id := n.ID()
		if visited[id] {
			continue
		}

		visited[id] = true

		if occupied[id] {
			return true
		}

		for _, in := range n.Incoming() {
			if src := in.Source(); src != nil {
				stack = append(stack, src)
			}
		}
	}

	return false
}

// interface checks
var (
	_ exec.FlowChecker = (*Instance)(nil)
	_ exec.FlowChecker = fixedFlowChecker{}
)
