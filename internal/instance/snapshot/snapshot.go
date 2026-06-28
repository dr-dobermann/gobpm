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

	s := Snapshot{
		ID:          *foundation.NewID(),
		ProcessID:   p.ID(),
		ProcessName: p.Name(),
		Nodes:       map[string]flow.Node{},
		Flows:       map[string]*flow.SequenceFlow{},
		Properties:  p.Properties(),
	}

	s.CorrelationKeys = correlationKeys(p)

	seExists := false
	eeExists := false
	instStartExists := false

	for _, n := range p.Nodes() {
		s.Nodes[n.ID()] = n

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
	if eeExists && !seExists && !instStartExists {
		return nil,
			errs.New(
				errs.M("no StartEvent or instantiating ReceiveTask in process " +
					"with an EndEvent"))
	}

	for _, f := range p.Flows() {
		s.Flows[f.ID()] = f
	}

	return &s, nil
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

// Clone returns a per-instance copy of the Snapshot. Every node is cloned (its
// immutable configuration shared by reference, its runtime state fresh) and the
// flow graph is relinked between the clones, so an instance built from the clone
// mutates only its own nodes. The immutable header — process id/name and
// properties — is shared. See ADR-009.
func (s *Snapshot) Clone() (*Snapshot, error) {
	clone := Snapshot{
		ID:              *foundation.NewID(),
		ProcessID:       s.ProcessID,
		ProcessName:     s.ProcessName,
		Nodes:           make(map[string]flow.Node, len(s.Nodes)),
		Flows:           make(map[string]*flow.SequenceFlow, len(s.Flows)),
		Properties:      s.Properties,
		CorrelationKeys: s.CorrelationKeys,
	}

	// 1. clone every node; the clone starts with empty flows and any default
	//    flow still points at the original edge (remapped in step 3).
	for id, n := range s.Nodes {
		clone.Nodes[id] = n.Clone()
	}

	// 2. relink the flow graph between the cloned nodes.
	for id, f := range s.Flows {
		src, ok := clone.Nodes[f.Source().ID()].(flow.SequenceSource)
		if !ok {
			return nil, errs.New(
				errs.M("cloned source %q isn't a sequence source",
					f.Source().ID()),
				errs.C(errorClass, errs.TypeCastingError))
		}

		trg, ok := clone.Nodes[f.Target().ID()].(flow.SequenceTarget)
		if !ok {
			return nil, errs.New(
				errs.M("cloned target %q isn't a sequence target",
					f.Target().ID()),
				errs.C(errorClass, errs.TypeCastingError))
		}

		// src and trg are cloned graph nodes and f is a valid edge, so the
		// edge can always be rebuilt; use the panicking form.
		clone.Flows[id] = flow.MustCloneFlow(f, src, trg)
	}

	// 3. remap each gateway's default flow onto its cloned edge.
	for _, n := range clone.Nodes {
		dfh, ok := n.(flow.DefaultFlowHolder)
		if !ok {
			continue
		}

		df := dfh.DefaultFlow()
		if df == nil {
			continue
		}

		// the default flow is one of this node's outgoing flows by
		// construction, so the remap onto its clone cannot fail.
		dfh.MustUpdateDefaultFlow(clone.Flows[df.ID()])
	}

	// 4. rebind each boundary event onto its cloned host activity. The cloned
	//    activities start with no boundaries (activity.clone leaves them for this
	//    step), so re-attaching the cloned boundary points BOTH cross-references
	//    (host→boundary and boundary→host) at this instance's own nodes — a
	//    boundary fire then acts on the instance graph, not the shared model
	//    (SRD-029 M3a). Iterating the originals gives the host mapping via the
	//    boundary's AttachedTo.
	for id, n := range s.Nodes {
		origBE, ok := n.(flow.BoundaryEvent)
		if !ok {
			continue
		}

		// The clones are cloned from valid model nodes — a BoundaryEvent's clone
		// is a BoundaryEvent and its host's clone an ActivityNode — so, as with the
		// flow relink above, these casts cannot fail (panicking form).
		cloneBE := clone.Nodes[id].(flow.BoundaryEvent)
		cloneHost := clone.Nodes[origBE.AttachedTo().ID()].(flow.ActivityNode)

		// The cloned host starts with no boundaries (activity.clone), so the
		// already-model-validated binding re-attaches without a multiplicity
		// conflict; an error here can only mean a corrupt clone.
		if err := cloneBE.BoundTo(cloneHost); err != nil {
			return nil, errs.New(
				errs.M("rebind boundary %q to its cloned host failed", id),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}
	}

	return &clone, nil
}
