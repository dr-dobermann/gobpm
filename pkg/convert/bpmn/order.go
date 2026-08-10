package bpmn

import (
	"cmp"
	"slices"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// orderNodes returns the process's nodes in a stable reading order:
// breadth-first from each start event, following outgoing sequence flows
// in flow-id order, then the same walk from every node the first pass did
// not reach, taken in id order.
//
// SOME order has to be imposed, because the model holds its nodes in a map
// and ranging over one is randomized — two exports of an unchanged process
// would otherwise differ, which makes an exported file undiffable and any
// golden test flaky (SRD-089.A §FR-3).
//
// Sorting by id would be deterministic too, and was this SRD's first
// answer. It produces a process document that opens with whatever id sorts
// first — for the repository's own example, the end event. Determinism was
// the requirement; alphabetical is merely the cheapest way to reach it, and
// an exported file is something people open.
//
// A graph with no start event, or one whose flows do not reach every node,
// degrades to id order for the unreached remainder rather than dropping it.
func orderNodes(nodes []flow.Node, flows []*flow.SequenceFlow) []flow.Node {
	if len(nodes) < 2 {
		return nodes
	}

	byID := make(map[string]flow.Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID()] = n
	}

	ordered := make([]flow.Node, 0, len(nodes))
	seen := make(map[string]bool, len(nodes))

	walk := func(root flow.Node, next map[string][]string) {
		queue := []flow.Node{root}

		for len(queue) > 0 {
			n := queue[0]
			queue = queue[1:]

			if seen[n.ID()] {
				continue
			}

			seen[n.ID()] = true
			ordered = append(ordered, n)

			for _, id := range next[n.ID()] {
				if trg, ok := byID[id]; ok && !seen[id] {
					queue = append(queue, trg)
				}
			}
		}
	}

	next := successors(flows)

	for _, n := range roots(nodes) {
		walk(n, next)
	}

	// Whatever the walks did not reach — a disconnected fragment, or a
	// graph with no start event at all — still has to be exported.
	for _, n := range sortedByID(nodes) {
		if !seen[n.ID()] {
			walk(n, next)
		}
	}

	return ordered
}

// successors maps each source node id to its target node ids, ordered by
// the id of the flow that joins them so a fan-out is emitted the same way
// on every run.
func successors(flows []*flow.SequenceFlow) map[string][]string {
	// Filter BEFORE sorting: the comparator dereferences its operands, so
	// sorting a slice that still holds a nil panics before any guard
	// inside the loop can run.
	type edge struct{ flowID, src, trg string }

	edges := make([]edge, 0, len(flows))

	for _, f := range flows {
		if f == nil {
			continue
		}

		src, trg := f.Source(), f.Target()
		if src == nil || trg == nil {
			continue
		}

		edges = append(edges, edge{flowID: f.ID(), src: src.ID(), trg: trg.ID()})
	}

	slices.SortFunc(edges, func(a, b edge) int {
		return cmp.Compare(a.flowID, b.flowID)
	})

	next := map[string][]string{}

	for _, e := range edges {
		next[e.src] = append(next[e.src], e.trg)
	}

	return next
}

// roots returns the start events, in id order — the points a reader of the
// document starts from.
func roots(nodes []flow.Node) []flow.Node {
	rr := make([]flow.Node, 0, len(nodes))

	for _, n := range nodes {
		if _, ok := n.(*events.StartEvent); ok {
			rr = append(rr, n)
		}
	}

	return sortedByID(rr)
}

// sortedByID returns nn ordered by BPMN id, without disturbing nn.
func sortedByID(nn []flow.Node) []flow.Node {
	sorted := slices.Clone(nn)
	slices.SortFunc(sorted, func(a, b flow.Node) int {
		return cmp.Compare(a.ID(), b.ID())
	})

	return sorted
}

// orderFlows returns the sequence flows in id order. Flows carry their
// endpoints in their own attributes, so their position in the document
// says nothing a reader needs — alphabetical is enough, and it is stable.
func orderFlows(flows []*flow.SequenceFlow) []*flow.SequenceFlow {
	sorted := slices.Clone(flows)
	slices.SortFunc(sorted, func(a, b *flow.SequenceFlow) int {
		if a == nil || b == nil {
			return 0
		}

		return cmp.Compare(a.ID(), b.ID())
	})

	return sorted
}
