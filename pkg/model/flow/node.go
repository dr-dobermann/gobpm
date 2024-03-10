package flow

import (
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type NodeType string

const (
	ActivityNode NodeType = "Activity"
	EventNode    NodeType = "Event"
	GatewayNode  NodeType = "Gateway"
)

type FlowNode interface {
	GetNode() *Node

	NodeType() NodeType
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
	incoming []*SequenceFlow

	// This attribute identifies the outgoing Sequence Flow of the FlowNode.
	// This is an ordered collection.
	outgoing []*SequenceFlow
}

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
			Element:  *e,
			incoming: []*SequenceFlow{},
			outgoing: []*SequenceFlow{}},
		nil
}

// Incoming returns a list of the Node's incoming sequence flows.
func (n Node) Incoming() []*SequenceFlow {
	return append(make([]*SequenceFlow, 0, len(n.incoming)), n.incoming...)
}

// Outgoing returns a list of the Node's outgoing sequence flows.
func (n Node) Outgoing() []*SequenceFlow {
	return append(make([]*SequenceFlow, 0, len(n.outgoing)), n.outgoing...)
}
