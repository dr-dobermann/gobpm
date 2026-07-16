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
