package flow

// LinkEventNode is implemented by an intermediate Link throw/catch event so the
// graph wiring can pair a Link source (throw) to its target (catch) by name
// within one container (ADR-006 v.4 §2.8, SRD-057). A node that carries no Link
// event definition returns "" from LinkName.
type LinkEventNode interface {
	Node

	// LinkName returns the Link pairing name, or "" when the node carries no
	// Link event definition.
	LinkName() string

	// IsLinkSource reports whether the node is the Link throw source (true) or
	// the catch target (false).
	IsLinkSource() bool
}

// LinkSource is a Link throw whose resolved target the wiring records; the
// throw's Exec then redirects the token to the target catch's outgoing flows.
type LinkSource interface {
	LinkEventNode

	// SetLinkTarget records the resolved target catch node.
	SetLinkTarget(target Node)
}

// resolveLinkEdges pairs every Link throw source in nodes to its same-name
// catch target and records the resolved target on the throw (ADR-006 v.4 §2.8,
// SRD-057). It runs as the last WireClonedGraph step, so it fires at every
// container level — the snapshot's top-level graph and each Sub-Process inner
// graph — with the pairing confined to that level's node set. Pairing is
// validated at registration (Process/SubProcess.Validate: exactly one target,
// ≥1 source per name), so this wiring assumes well-formed input; an unpaired
// source leaves its target nil, which the throw's Exec reports.
func resolveLinkEdges(nodes map[string]Node) {
	targets := map[string]Node{}

	var sources []LinkSource

	for _, n := range nodes {
		ln, ok := n.(LinkEventNode)
		if !ok || ln.LinkName() == "" {
			continue
		}

		if ln.IsLinkSource() {
			if ls, ok := n.(LinkSource); ok {
				sources = append(sources, ls)
			}

			continue
		}

		targets[ln.LinkName()] = n
	}

	for _, s := range sources {
		if t, ok := targets[s.LinkName()]; ok {
			s.SetLinkTarget(t)
		}
	}
}
