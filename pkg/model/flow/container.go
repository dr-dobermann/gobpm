package flow

import (
	"errors"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// Container is an abstract super class for BPMN diagrams (or
// views) and defines the superset of elements that are contained in those
// diagrams. Basically, a ElementsContainer contains BaseElements, which
// are Events, Gateways, Sequence Flows, Activities, and Choreography
// Activities.
//
// There are four (4) types of ElementsContainers: Process, Sub-Process,
// Choreography, and Sub-Choreography.
type Container interface {
	foundation.BaseObject

	Add(Element) error
	Remove(Element) error

	Elements() []Element
}

// ElementsContainer is the embeddable graph-holding core of a Container
// (ADR-023 §2.2): id-keyed nodes and sequence flows with the Add/Remove
// dispatch and the flow-endpoint validation every container level shares.
// It carries NO element identity of its own — the embedding owner (a
// Process, a Sub-Process) provides foundation.BaseObject, and together
// they satisfy the Container interface.
//
// The container the elements bind to (Element.BindTo) is the OWNER, not
// this core — the owner passes itself to Add via the self parameter, so
// the same-container rule of SequenceFlow.Validate confines a nested
// graph exactly like a Process's.
type ElementsContainer struct {
	nodes map[string]Node
	flows map[string]*SequenceFlow
}

// NewElementsContainer creates an empty graph core.
func NewElementsContainer() ElementsContainer {
	return ElementsContainer{
		nodes: map[string]Node{},
		flows: map[string]*SequenceFlow{},
	}
}

// AddElement adds a flow element into the container core, binding it to
// owner (the embedding Container). Dispatches by element type: nodes and
// sequence flows are accepted, anything else is a classified error.
func (c *ElementsContainer) AddElement(owner Container, e Element) error {
	if owner == nil {
		return errs.New(
			errs.M("AddElement: a nil owner container isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if e == nil {
		return errs.New(
			errs.M("AddElement: a nil flow element isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	switch e.EType() {
	case NodeElement:
		n, ok := e.(Node)
		if !ok {
			return errs.New(
				errs.M("element %q reports NodeElement type but is not a Node",
					e.ID()),
				errs.C(errorClass, errs.TypeCastingError))
		}

		return c.addNode(owner, n)

	case SequenceBaseElement:
		f, ok := e.(*SequenceFlow)
		if !ok {
			return errs.New(
				errs.M("element %q reports SequenceBaseElement type but is "+
					"not a *SequenceFlow", e.ID()),
				errs.C(errorClass, errs.TypeCastingError))
		}

		return c.addFlow(owner, f)
	}

	return errs.New(
		errs.M("invalid flow element type: %s", string(e.EType())),
		errs.C(errorClass, errs.InvalidParameter))
}

// addNode registers a node in the core after binding it to the owner.
func (c *ElementsContainer) addNode(owner Container, n Node) error {
	if _, ok := c.nodes[n.ID()]; ok {
		return errs.New(
			errs.M("node %q is already in the container", n.ID()),
			errs.C(errorClass, errs.DuplicateObject))
	}

	if err := n.BindTo(owner); err != nil {
		return err
	}

	c.nodes[n.ID()] = n

	return nil
}

// addFlow registers a sequence flow in the core after binding it to the
// owner.
func (c *ElementsContainer) addFlow(owner Container, f *SequenceFlow) error {
	if _, ok := c.flows[f.ID()]; ok {
		return errs.New(
			errs.M("flow %q is already in the container", f.ID()),
			errs.C(errorClass, errs.DuplicateObject))
	}

	if err := f.BindTo(owner); err != nil {
		return err
	}

	c.flows[f.ID()] = f

	return nil
}

// RemoveElement removes a flow element from the core and unbinds it.
func (c *ElementsContainer) RemoveElement(e Element) error {
	if e == nil {
		return errs.New(
			errs.M("RemoveElement: a nil flow element isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	switch {
	case c.nodes[e.ID()] != nil:
		delete(c.nodes, e.ID())

	case c.flows[e.ID()] != nil:
		delete(c.flows, e.ID())

	default:
		return errs.New(
			errs.M("element %q isn't in the container", e.ID()),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	return e.Unbind()
}

// Elements returns all the core's elements (nodes and flows).
func (c *ElementsContainer) Elements() []Element {
	ee := make([]Element, 0, len(c.nodes)+len(c.flows))

	for _, n := range c.nodes {
		ee = append(ee, n)
	}

	for _, f := range c.flows {
		ee = append(ee, f)
	}

	return ee
}

// Nodes returns the core's nodes.
func (c *ElementsContainer) Nodes() []Node {
	nn := make([]Node, 0, len(c.nodes))

	for _, n := range c.nodes {
		nn = append(nn, n)
	}

	return nn
}

// Flows returns the core's sequence flows.
func (c *ElementsContainer) Flows() []*SequenceFlow {
	ff := make([]*SequenceFlow, 0, len(c.flows))

	for _, f := range c.flows {
		ff = append(ff, f)
	}

	return ff
}

// ValidateFlows checks that every sequence flow's source and target are
// nodes of THIS container — the per-level endpoint-membership rule
// (ADR-023 §2.2; the Process.Validate loop's container twin).
func (c *ElementsContainer) ValidateFlows() error {
	ee := []error{}

	for id, f := range c.flows {
		if _, ok := c.nodes[f.Source().ID()]; !ok {
			ee = append(ee, errs.New(
				errs.M("source %q of sequence flow %q is not in the container",
					f.Source().ID(), id),
				errs.C(errorClass, errs.ObjectNotFound)))
		}

		if _, ok := c.nodes[f.Target().ID()]; !ok {
			ee = append(ee, errs.New(
				errs.M("target %q of sequence flow %q is not in the container",
					f.Target().ID(), id),
				errs.C(errorClass, errs.ObjectNotFound)))
		}
	}

	if len(ee) > 0 {
		return errors.Join(ee...)
	}

	return nil
}

// WireClonedGraph completes a freshly cloned node set into a runnable
// graph: it relinks the flow graph between the clones, remaps each
// gateway's default flow onto its cloned edge, and rebinds each boundary
// event onto its cloned host activity. One wiring implementation serves
// every container level — the snapshot's top-level graph and a
// Sub-Process's inner graph (CloneGraph) alike. clonedNodes is the
// already-cloned node set (mutated in place for the default-flow and
// boundary rebinds); srcNodes and srcFlows are the originals the clones
// were made from. It returns the cloned flow set.
func WireClonedGraph(
	clonedNodes map[string]Node,
	srcNodes map[string]Node,
	srcFlows map[string]*SequenceFlow,
) (map[string]*SequenceFlow, error) {
	clonedFlows := make(map[string]*SequenceFlow, len(srcFlows))

	// 1. relink the flow graph between the cloned nodes.
	for id, f := range srcFlows {
		src, ok := clonedNodes[f.Source().ID()].(SequenceSource)
		if !ok {
			return nil, errs.New(
				errs.M("cloned source %q isn't a sequence source",
					f.Source().ID()),
				errs.C(errorClass, errs.TypeCastingError))
		}

		trg, ok := clonedNodes[f.Target().ID()].(SequenceTarget)
		if !ok {
			return nil, errs.New(
				errs.M("cloned target %q isn't a sequence target",
					f.Target().ID()),
				errs.C(errorClass, errs.TypeCastingError))
		}

		// src and trg are cloned graph nodes and f is a valid edge, so the
		// edge can always be rebuilt; use the panicking form.
		clonedFlows[id] = MustCloneFlow(f, src, trg)
	}

	// 2. remap each gateway's default flow onto its cloned edge.
	for _, n := range clonedNodes {
		dfh, ok := n.(DefaultFlowHolder)
		if !ok {
			continue
		}

		df := dfh.DefaultFlow()
		if df == nil {
			continue
		}

		// the default flow is one of this node's outgoing flows by
		// construction, so the remap onto its clone cannot fail.
		dfh.MustUpdateDefaultFlow(clonedFlows[df.ID()])
	}

	// 3. rebind each boundary event onto its cloned host activity. The cloned
	//    activities start with no boundaries (the clone contract leaves them
	//    for this step), so re-attaching the cloned boundary points BOTH
	//    cross-references (host→boundary and boundary→host) at the cloned
	//    nodes — a boundary fire then acts on this graph, not the shared
	//    source (SRD-029 M3a). Iterating the originals gives the host mapping
	//    via the boundary's AttachedTo.
	for id, n := range srcNodes {
		origBE, ok := n.(BoundaryEvent)
		if !ok {
			continue
		}

		// The clones are cloned from valid nodes — a BoundaryEvent's clone is
		// a BoundaryEvent and its host's clone an ActivityNode — so, as with
		// the flow relink above, these casts cannot fail (panicking form).
		cloneBE := clonedNodes[id].(BoundaryEvent)
		cloneHost := clonedNodes[origBE.AttachedTo().ID()].(ActivityNode)

		// The cloned host starts with no boundaries, so the already-validated
		// binding re-attaches without a multiplicity conflict; an error here
		// can only mean a corrupt clone.
		if err := cloneBE.BoundTo(cloneHost); err != nil {
			return nil, errs.New(
				errs.M("rebind boundary %q to its cloned host failed", id),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}
	}

	// 4. resolve Link edges: pair each Link throw source to its same-name catch
	//    target within this cloned node set and record the resolved target on
	//    the throw (ADR-006 v.4 §2.8, SRD-057). Confined to this container level
	//    — the "single Process level" rule — because WireClonedGraph runs per
	//    level (top-level snapshot and each Sub-Process inner graph).
	resolveLinkEdges(clonedNodes)

	return clonedFlows, nil
}

// CloneGraph returns a deep per-instance copy of the core's graph: every
// node cloned via its own Clone (a nested container recurses naturally),
// the flows relinked, defaults remapped and boundaries rebound between the
// clones (WireClonedGraph). The cloned elements carry no container
// back-reference — the Clone contract; containment matters at build time,
// and a cloned graph exists to be executed, not edited.
func (c *ElementsContainer) CloneGraph() (ElementsContainer, error) {
	cloned := make(map[string]Node, len(c.nodes))

	for id, n := range c.nodes {
		cn, err := n.Clone()
		if err != nil {
			return ElementsContainer{}, errs.New(
				errs.M("couldn't clone node %q", id),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
		}

		cloned[id] = cn
	}

	flows, err := WireClonedGraph(cloned, c.nodes, c.flows)
	if err != nil {
		return ElementsContainer{}, err
	}

	return ElementsContainer{nodes: cloned, flows: flows}, nil
}
