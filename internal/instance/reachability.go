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
	occupied := inst.occupiedNodes()
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

// occupiedNodes is the set of node ids that currently hold a live token. It uses
// the projected active tokens (Alive or WaitForEvent), so a track parked at a
// synchronizing join is included — it may still resume and reach a downstream
// flow — while a dead track (Consumed) is excluded.
func (inst *Instance) occupiedNodes() map[string]bool {
	occupied := map[string]bool{}

	for _, tok := range inst.GetTokens() {
		if tok.Node != nil {
			occupied[tok.Node.ID()] = true
		}
	}

	return occupied
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

// interface check
var _ exec.FlowChecker = (*Instance)(nil)
