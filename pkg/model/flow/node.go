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

// The FlowNode element is used to provide a single element as the source and
// target Sequence Flow associations instead of the individual associations of
// the elements that can connect to Sequence Flows.
// Only the Gateway, Activity, Choreography Activity, and Event elements can
// connect to Sequence Flows and thus, these elements are the only ones that
// are sub-classes of FlowNode.
type Node struct {
	Element

	// This attribute identifies the incoming Sequence Flow of the FlowNode.
	incoming map[string]*SequenceFlow

	// This attribute identifies the outgoing Sequence Flow of the FlowNode.
	// This is an ordered collection.
	outgoing map[string]*SequenceFlow
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
			incoming: map[string]*SequenceFlow{},
			outgoing: map[string]*SequenceFlow{}},
		nil
}

// Incoming returns a list of the Node's incoming sequence flows.
func (n *Node) Incoming() []*SequenceFlow {
	ii := make([]*SequenceFlow, 0, len(n.incoming))
	for _, in := range n.incoming {
		ii = append(ii, in)
	}
	return ii
}

// Outgoing returns a list of the Node's outgoing sequence flows.
func (n *Node) Outgoing() []*SequenceFlow {
	oo := make([]*SequenceFlow, 0, len(n.outgoing))
	for _, o := range n.outgoing {
		oo = append(oo, o)
	}

	return oo
}

// GetNode implements FlowNode for all its chields.
func (n *Node) GetNode() *Node {
	return n
}

// addIncoming add singe non-empty sequence flow into the Node's incoming flows.
func (n *Node) addIncoming(sf *SequenceFlow) {
	if sf != nil {
		n.incoming[sf.Id()] = sf
	}
}

// delIncoming deletes non-empyt SequenceFlow from the Node's incoming flows.
// func (n *Node) delIncoming(sf *SequenceFlow) {
// 	if sf != nil {
// 		delete(n.incoming, sf.Id())
// 	}
// }

// addOutgoing adds singe non-empty sequence flow into the Node's
// outgoing flows.
func (n *Node) addOutgoing(sf *SequenceFlow) {
	if sf != nil {
		n.outgoing[sf.Id()] = sf
	}
}

// delOutgoing removes singe non-empty sequence flow from the Node's
// outgoing flows.
// func (n *Node) delOutgoing(sf *SequenceFlow) {
// 	if sf != nil {
// 		delete(n.outgoing, sf.Id())
// 	}
// }
