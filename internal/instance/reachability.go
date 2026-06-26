package instance

import (
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

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
// occupied-node snapshot built from the loop-owned position view (SRD-028 §3.4).
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

// joinPositions derives, from the loop-owned position/parked maps, the occupied-node set used
// for reachability AND whether a token is imminently arriving at joinNode (sitting on the join
// but not yet parked there). It reads ONLY the loop's own maps — never another track's
// currentStep/inState cross-goroutine (SRD-028 FR-4) — which is what makes the recheck race-free:
//
//   - position holds every LIVE track's current node (a dead track was dropped on its
//     evEnded/evFailed/evMerged), so every entry counts as occupied — a freshly forked track
//     too, seeded at spawn before it records any history;
//   - a token on joinNode that is not in parked is between its evMoved onto the join and its
//     evParked — an imminent arrival the caller must wait for, not decide around. A parked
//     entry at the join is a settled arrival (TrackAwaitSync), not in transit.
//
// joinNode may be nil (plain occupied-set query); then inTransit is always false.
func joinPositions(
	joinNode flow.Node,
	position, parked map[string]flow.Node,
) (occupied map[string]bool, inTransit bool) {
	occupied = make(map[string]bool, len(position))

	for id, n := range position {
		occupied[n.ID()] = true

		if joinNode != nil && n.ID() == joinNode.ID() {
			if _, isParked := parked[id]; !isParked {
				inTransit = true
			}
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

// interface check — fixedFlowChecker is the only FlowChecker (the live inst.CheckFlows path
// was removed with SRD-028: every recheck builds a snapshot from the loop-owned position view).
var _ exec.FlowChecker = fixedFlowChecker{}
