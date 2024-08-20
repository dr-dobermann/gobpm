package flow

import (
	"errors"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"golang.org/x/exp/maps"
)

type NodeType string

const (
	ActivityNodeType NodeType = "Activity"
	EventNodeType    NodeType = "Event"
	GatewayNodeType  NodeType = "Gateway"
)

// Validate checks if nt has NodeType value.
func (nt NodeType) Validate() error {
	if nt != ActivityNodeType &&
		nt != EventNodeType &&
		nt != GatewayNodeType {
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
type Node interface {
	Element

	// This attribute identifies the incoming Sequence Flow of the FlowNode.
	// incoming map[string]*SequenceFlow

	// This attribute identifies the outgoing Sequence Flow of the FlowNode.
	// This is an ordered collection.
	// outgoing map[string]*SequenceFlow

	// flows holds both incoming and outgoing flows of the Node.
	Incoming() []*SequenceFlow
	Outgoing() []*SequenceFlow

	AddFlow(*SequenceFlow, data.Direction) error

	NodeType() NodeType

	// Node returns underlying node object.
	Node() Node
}

// *****************************************************************************

// FlowNode provides base functionality of all Nodes as sequence flows holder.
type FlowNode struct {
	flows map[data.Direction]map[string]*SequenceFlow
}

func NewFlowNode() *FlowNode {
	return &FlowNode{
		flows: map[data.Direction]map[string]*SequenceFlow{},
	}
}

// --------------------- Node interface ----------------------------------------

// Incoming returns all the FlowNode's incoming flows.
func (fn *FlowNode) Incoming() []*SequenceFlow {
	return maps.Values(fn.flows[data.Input])
}

// Outgoing returns all the FlowNodes outgoing flows.
func (fn *FlowNode) Outgoing() []*SequenceFlow {
	return maps.Values(fn.flows[data.Output])
}

// AddFlow adds new SequenceFlow to the FlowNode n.
func (n *FlowNode) AddFlow(sf *SequenceFlow, dir data.Direction) error {
	if sf == nil {
		return errs.New(
			errs.M("couldn't add empty sequence flow"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if err := dir.Validate(); err != nil {
		return err
	}

	if _, ok := n.flows[dir]; !ok {
		n.flows[dir] = map[string]*SequenceFlow{}
	}

	n.flows[dir][sf.Id()] = sf

	return nil
}

// ----------------- Element interface -----------------------------------------

// Type returns ElementType of the FlowNode.
func (n *FlowNode) Type() ElementType {
	return NodeElement
}

// -----------------------------------------------------------------------------
