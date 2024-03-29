package flow

import (
	"errors"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

type NodeType string

const (
	ActivityNodeType NodeType = "Activity"
	EventNodeType    NodeType = "Event"
	GatewayNodeType  NodeType = "Gateway"
)

type EventNode interface {
	Node

	EventType() string
}

type ActivityNode interface {
	Node

	ActivityType() string
}

type GatewayNode interface {
	Node

	GatewayType() string
}

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
}

// *****************************************************************************
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
	return common.Map2List(fn.flows[data.Input])
}

// Outgoing returns all the FlowNodes outgoing flows.
func (fn *FlowNode) Outgoing() []*SequenceFlow {
	return common.Map2List(fn.flows[data.Output])
}

// AddFlow adds new SequenceFlow to the FlowNode n.
func (n *FlowNode) AddFlow(sf *SequenceFlow, dir data.Direction) error {
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

// ----------------- Element interface -----------------------------------------

// Type returns ElementType of the FlowNode.
func (n *FlowNode) Type() ElementType {
	return NodeElement
}

// -----------------------------------------------------------------------------

// removeFlow deletes single sequence flow from the node.
// func (n *FlowNode) removeFlow(sf *SequenceFlow, dir data.Direction) error {
// 	if err := dir.Validate(); err != nil {
// 		return err
// 	}

// 	if sf == nil {
// 		return errs.New(
// 			errs.M("sequence flow couldn't be empty"),
// 			errs.C(errorClass, errs.EmptyNotAllowed))
// 	}

// 	if _, ok := n.flows[dir]; !ok {
// 		return errs.New(
// 			errs.M("node has no %s flows", dir),
// 			errs.C(errorClass, errs.InvalidObject))
// 	}

// 	delete(n.flows[dir], sf.Id())

// 	// invalidate the SequenceFlow
// 	switch dir {
// 	case data.Input:
// 		sf.target = nil

// 	case data.Output:
// 		sf.source = nil
// 	}

// 	return nil
// }
