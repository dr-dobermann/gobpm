// Package snapshot provides process instance snapshot functionality.
package snapshot

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
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

	s := Snapshot{
		ID:          *foundation.NewID(),
		ProcessID:   p.ID(),
		ProcessName: p.Name(),
		Nodes:       map[string]flow.Node{},
		Flows:       map[string]*flow.SequenceFlow{},
		Properties:  p.Properties(),
	}

	seExists := false
	eeExists := false

	for _, n := range p.Nodes() {
		s.Nodes[n.ID()] = n

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

	// by BPMN demands that, if there is an EndEvent in the process, there should
	// be at least one StartEvent
	if eeExists && !seExists {
		return nil,
			errs.New(
				errs.M("no StartEvent in process with an EndEvent"))
	}

	for _, f := range p.Flows() {
		s.Flows[f.ID()] = f
	}

	return &s, nil
}

// Clone returns a per-instance copy of the Snapshot. Every node is cloned (its
// immutable configuration shared by reference, its runtime state fresh) and the
// flow graph is relinked between the clones, so an instance built from the clone
// mutates only its own nodes. The immutable header — process id/name and
// properties — is shared. See ADR-009.
func (s *Snapshot) Clone() (*Snapshot, error) {
	clone := Snapshot{
		ID:          *foundation.NewID(),
		ProcessID:   s.ProcessID,
		ProcessName: s.ProcessName,
		Nodes:       make(map[string]flow.Node, len(s.Nodes)),
		Flows:       make(map[string]*flow.SequenceFlow, len(s.Flows)),
		Properties:  s.Properties,
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

	return &clone, nil
}
