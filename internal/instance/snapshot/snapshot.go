// Package snapshot provides process instance snapshot functionality.
package snapshot

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

const errorClass = "SNAPSHOT_ERRORS"

// Snapshot holds process'es snapshot ready to run.
type Snapshot struct {
	foundation.ID

	ProcessID   string
	ProcessName string
	Nodes       map[string]flow.Node
	Flows       map[string]*flow.SequenceFlow
	Properties  []*data.Property
	// CorrelationKeys are the process's declared correlation keys (the Key of
	// each CorrelationSubscription). An in-instance receiver derives them from
	// an incoming message's payload to grow the instance's conversation key-set
	// (lazy association — SRD-017 §4.5). Immutable config, shared by Clone.
	CorrelationKeys []*bpmncommon.CorrelationKey
	// InstantiatingStarts are the process's instantiating start triggers
	// (message / signal StartEvents and instantiate ReceiveTasks), discovered
	// once by New after the graph is wired. The thresher wraps each into a
	// persistent instance-starter at registration instead of re-scanning the
	// node graph. Engine-agnostic descriptors only; immutable, shared by Clone.
	InstantiatingStarts []InstantiatingStart
	// HasConditionals reports whether any node carries a Conditional event
	// definition (catch, boundary, or event-based-gateway arm), precomputed
	// once by New (SRD-048): a track emits the commit-diff signal to the
	// instance loop only when true, so a conditional-free process never pays
	// for it. Immutable, shared by Clone.
	HasConditionals bool
}

// New creates a new snapshot from the Process p and returns its
// pointer on success or error on failure.
func New(
	p *process.Process,
	_ ...options.Option,
) (*Snapshot, error) {
	if p == nil {
		return nil,
			errs.New(
				errs.M("process is empty"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	// validate the process graph before taking the snapshot, so a malformed
	// process (a sequence flow whose source or target is not in the process)
	// is rejected at registration rather than producing a broken snapshot.
	if err := p.Validate(); err != nil {
		return nil, err
	}

	// Clone the process properties into the snapshot so the frozen template owns
	// private property objects — like the node graph cloned below — and a process
	// edit after registration can't reach the registered version (FIX-016,
	// ADR-019 §2.3).
	props, err := data.CloneProperties(p.Properties())
	if err != nil {
		return nil, errs.New(
			errs.M("couldn't clone process properties into snapshot"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	s := Snapshot{
		ID:          *foundation.NewID(),
		ProcessID:   p.ID(),
		ProcessName: p.Name(),
		Nodes:       map[string]flow.Node{},
		Properties:  props,
	}

	s.CorrelationKeys = correlationKeys(p)

	seExists := false
	eeExists := false
	instStartExists := false

	// srcNodes keeps the original process nodes for wireClonedGraph's boundary
	// rebind; s.Nodes gets their clones so the snapshot owns an isolated graph
	// (ADR-019 §2.3) — edits to the process after registration can't reach it.
	srcNodes := make(map[string]flow.Node, len(p.Nodes()))

	for _, n := range p.Nodes() {
		srcNodes[n.ID()] = n

		cn, cerr := cloneNode(n)
		if cerr != nil {
			return nil, cerr
		}

		s.Nodes[n.ID()] = cn

		// An instantiate ReceiveTask with no incoming flow is a valid process
		// instantiation point on its own (BPMN §13.3.3) — the task-shaped peer
		// of a message start event.
		if isInstantiatingTask(n) {
			instStartExists = true
		}

		// check events
		if n.NodeType() == flow.EventNodeType {
			en, ok := n.(flow.EventNode)
			if !ok {
				return nil,
					errs.New(
						errs.M("failed to convert to EventNode"),
						errs.C(errorClass, errs.TypeCastingError),
						errs.D("node_id", n.ID()),
						errs.D("node_name", n.Name()))
			}

			switch en.EventClass() {
			case flow.StartEventClass:
				seExists = true

			case flow.IntermediateEventClass:
				break

			case flow.EndEventClass:
				eeExists = true
			}
		}
	}

	// BPMN requires that, if there is an EndEvent, the process has an
	// instantiation point: a StartEvent or a no-incoming instantiate
	// ReceiveTask (§13.3.3).
	if !hasInstantiationPoint(seExists, eeExists, instStartExists) {
		return nil,
			errs.New(
				errs.M("no StartEvent or instantiating ReceiveTask in process " +
					"with an EndEvent"))
	}

	srcFlows := make(map[string]*flow.SequenceFlow, len(p.Flows()))
	for _, f := range p.Flows() {
		srcFlows[f.ID()] = f
	}

	// Wire the cloned graph the same way Clone does — relink flows between the
	// clones, remap default flows, rebind boundary events — so the snapshot is
	// born isolated in a single pass over the definition (SRD-031.A §3.3).
	flows, err := flow.WireClonedGraph(s.Nodes, srcNodes, srcFlows)
	if err != nil {
		return nil, err
	}

	s.Flows = flows

	// With the graph wired (each clone's Incoming() now populated), discover the
	// instantiating start triggers once and store them, so registration wraps the
	// descriptors into starters instead of re-walking the node graph.
	s.InstantiatingStarts = discoverInstantiatingStarts(s.Nodes)
	s.HasConditionals = hasConditionals(s.Nodes)

	return &s, nil
}

// hasConditionals reports whether any event node — an intermediate catch, a
// boundary event, or an event-based-gateway arm (all process nodes) — carries
// a Conditional definition (SRD-048). One pass at snapshot build; the flag
// gates the per-commit evDataCommit emit.
func hasConditionals(nodes map[string]flow.Node) bool {
	for _, n := range nodes {
		en, ok := n.(flow.EventNode)
		if !ok {
			continue
		}

		for _, d := range en.Definitions() {
			if d.Type() == flow.TriggerConditional {
				return true
			}
		}
	}

	return false
}

// correlationKeys extracts the process's declared correlation keys — the Key of
// each non-nil CorrelationSubscription — for the snapshot (SRD-017 §4.5). An
// in-instance receiver derives these from an incoming message to grow its
// conversation key-set.
func correlationKeys(p *process.Process) []*bpmncommon.CorrelationKey {
	keys := make([]*bpmncommon.CorrelationKey, 0, len(p.CorrelationSubscriptions))

	for _, cs := range p.CorrelationSubscriptions {
		if cs != nil && cs.Key != nil {
			keys = append(keys, cs.Key)
		}
	}

	return keys
}

// isInstantiatingTask reports whether n is a no-incoming instantiate
// ReceiveTask — a valid process instantiation point on its own (BPMN §13.3.3).
// Matched structurally to avoid coupling the snapshot to the activities package.
func isInstantiatingTask(n flow.Node) bool {
	rt, ok := n.(interface{ Instantiate() bool })

	return ok && rt.Instantiate() && len(n.Incoming()) == 0
}

// hasInstantiationPoint reports whether the process can be instantiated for
// BPMN's rule that a process containing an EndEvent must have an instantiation
// point — a StartEvent or a no-incoming instantiate ReceiveTask (§13.3.3).
func hasInstantiationPoint(seExists, eeExists, instStartExists bool) bool {
	return !eeExists || seExists || instStartExists
}

// cloneNode returns a clone of process node n, wrapping a clone failure with the
// node's identity. A node clone fails when a property is value-less and thus
// unclonable (FIX-017); a node without properties never errors.
func cloneNode(n flow.Node) (flow.Node, error) {
	cn, err := n.Clone()
	if err != nil {
		return nil, errs.New(
			errs.M("couldn't clone node %q", n.ID()),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	return cn, nil
}

// Clone returns a per-instance copy of the Snapshot. Every node is cloned (its
// immutable configuration shared by reference, its runtime state fresh) and the
// flow graph is relinked between the clones, so an instance built from the clone
// mutates only its own nodes. Properties are cloned too — they carry per-instance
// mutable runtime state, so each instance owns its own (FIX-016). The genuinely
// immutable header — process id/name, correlation-key definitions and
// instantiating-start descriptors — is shared by reference. See ADR-009.
func (s *Snapshot) Clone() (*Snapshot, error) {
	props, err := data.CloneProperties(s.Properties)
	if err != nil {
		return nil, err
	}

	clone := Snapshot{
		ID:                  *foundation.NewID(),
		ProcessID:           s.ProcessID,
		ProcessName:         s.ProcessName,
		Nodes:               make(map[string]flow.Node, len(s.Nodes)),
		Properties:          props,
		CorrelationKeys:     s.CorrelationKeys,
		InstantiatingStarts: s.InstantiatingStarts,
		HasConditionals:     s.HasConditionals,
	}

	// Clone every node (its immutable configuration shared by reference, its
	// runtime state fresh); the clone starts with empty flows and any default
	// flow still points at the original edge until wireClonedGraph remaps it.
	for id, n := range s.Nodes {
		cn, cerr := cloneNode(n)
		if cerr != nil {
			return nil, cerr
		}

		clone.Nodes[id] = cn
	}

	flows, err := flow.WireClonedGraph(clone.Nodes, s.Nodes, s.Flows)
	if err != nil {
		return nil, err
	}

	clone.Flows = flows

	return &clone, nil
}

