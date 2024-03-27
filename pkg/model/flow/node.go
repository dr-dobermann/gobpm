package flow

import (
	"errors"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type NodeType string

const (
	ActivityNode NodeType = "Activity"
	EventNode    NodeType = "Event"
	GatewayNode  NodeType = "Gateway"
)

// Validate checks if nt has NodeType value.
func (nt NodeType) Validate() error {
	if nt != ActivityNode &&
		nt != EventNode &&
		nt != GatewayNode {
		return errs.New(
			errs.M("invalid NodeType: %q", nt),
			errs.C(errorClass, errs.TypeCastingError))
	}

	return nil
}

// ValidateNodeTypes checks list of NodeTypes on validity.
func ValidateNodeTypes(types ...NodeType) error {
	ee := []error{}

	for _, t := range types {
		if err := t.Validate(); err != nil {
			ee = append(ee, err)
		}
	}

	if len(ee) != 0 {
		return errors.Join(ee...)
	}

	return nil
}

// The FlowNode element is used to provide a single element as the source and
// target Sequence Flow associations instead of the individual associations of
// the elements that can connect to Sequence Flows.
// Only the Gateway, Activity, Choreography Activity, and Event elements can
// connect to Sequence Flows and thus, these elements are the only ones that
// are sub-classes of FlowNode.
type Node struct {
	Element

	// This attribute identifies the incoming Sequence Flow of the FlowNode.
	// incoming map[string]*SequenceFlow

	// This attribute identifies the outgoing Sequence Flow of the FlowNode.
	// This is an ordered collection.
	// outgoing map[string]*SequenceFlow

	// flows holds both incoming and outgoing flows of the Node.
	flows map[data.Direction]map[string]*SequenceFlow
}

// ------------------- FlowElement interface -----------------------------------

// ElementType returns the Node's element type.
func (n *Node) ElementType() ElementType {
	return NodeElement
}

// GetElement returns underlaying Element.
func (f *Node) GetElement() *Element {
	return &f.Element
}

// -----------------------------------------------------------------------------

// NewNode creates a new node and returns its pointer.
func NewNode(
	name string,
	baseOpts ...options.Option,
) (*Node, error) {
	e, err := NewElement(name, baseOpts...)
	if err != nil {
		return nil, err
	}

	return &Node{
			Element: *e,
			flows:   map[data.Direction]map[string]*SequenceFlow{},
		},
		nil
}

// MustNode creates a new Node and returns its pointer on succes or panics on
// failure.
func MustNode(
	name string,
	baseOpts ...options.Option,
) *Node {
	n, err := NewNode(name, baseOpts...)
	if err != nil {
		errs.Panic(err)
	}

	return n
}

// Incoming returns a list of the Node's incoming sequence flows.
func (n *Node) Incoming() []*SequenceFlow {
	ii, ok := n.flows[data.Input]
	if !ok || len(ii) == 0 {
		return []*SequenceFlow{}
	}

	res := make([]*SequenceFlow, 0, len(ii))
	for _, in := range ii {
		res = append(res, in)
	}

	return res
}

// Outgoing returns a list of the Node's outgoing sequence flows.
func (n *Node) Outgoing() []*SequenceFlow {
	ii, ok := n.flows[data.Output]
	if !ok || len(ii) == 0 {
		return []*SequenceFlow{}
	}

	res := make([]*SequenceFlow, 0, len(ii))
	for _, in := range ii {
		res = append(res, in)
	}

	return res
}

// GetNode implements FlowNode for all its chields.
func (n *Node) GetNode() *Node {
	return n
}

// addIncoming add singe non-empty sequence flow into the Node's incoming flows.
func (n *Node) addFlow(sf *SequenceFlow, dir data.Direction) error {
	if err := dir.Validate(); err != nil {
		return err
	}

	if _, ok := n.flows[dir]; !ok {
		n.flows[dir] = map[string]*SequenceFlow{}
	}

	if sf != nil {
		n.flows[dir][sf.Id()] = sf
	}

	return nil
}

// removeFlow deletes single sequence flow from the node.
func (n *Node) removeFlow(sf *SequenceFlow, dir data.Direction) error {
	if err := dir.Validate(); err != nil {
		return err
	}

	if _, ok := n.flows[dir]; !ok {
		return errs.New(
			errs.M("node %q has no %s flows", n.name, dir),
			errs.C(errorClass, errs.InvalidObject))
	}

	if sf == nil {
		return errs.New(
			errs.M("sequence flow couldn't be empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	delete(n.flows[dir], sf.Id())

	// invalidate the SequenceFlow
	switch dir {
	case data.Input:
		sf.target = nil

	case data.Output:
		sf.source = nil
	}

	return nil
}
