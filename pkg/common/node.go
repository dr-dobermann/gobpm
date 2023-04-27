package common

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/identity"
)

type Node interface {
	ID() identity.Id
	Name() string
	Type() FlowElementType
	LaneName() string
	ProcessID() identity.Id
	//PutOnLane(lane *Lane) error
	Connect(fn Node, sName string) (SequenceFlow, error)

	HasIncoming() bool

	// deletes all incoming and outcoming flows when copying the node
	// only calls from proccess.Copy method to avoid duplication flows
	// on copied node.
	//
	// DO NOT CALL directly!
	//
	ClearFlows()
}

// base for Activities, Gates and Events
type FlowNode struct {
	FlowElement

	incoming  []*SequenceFlow
	outcoming []*SequenceFlow
}

// GetOutputFlows returns node's output flows
func (fn *FlowNode) GetOutputFlows() []*SequenceFlow {
	res := make([]*SequenceFlow, 0)
	if fn.outcoming != nil {
		res = append(res, fn.outcoming...)
	}

	return res
}

// Connect connects fn with target FlowNode and return new SequenceFlow
// named flowName if there is no duplication.
func (fn *FlowNode) Connect(target *FlowNode, flowName string) (*SequenceFlow, error) {

	if target == nil {
		return nil,
			fmt.Errorf("couldn't connect %s[%v] with nil node", fn.name, fn.ID())
	}

	// check for duplicates
	for _, f := range fn.outcoming {
		if f.targetRef.ID() == target.ID() {
			return nil,
				fmt.Errorf("sequence flow %v already connects node %s[%v] to node %s[%v]",
					f.ID(), fn.name, fn.ID(), target.name, target.ID())
		}
	}

	sf := &SequenceFlow{
		FlowElement: *NewElement(identity.EmptyID(), flowName, EtSequenceFlow),
		expr:        nil,
		sourceRef:   fn,
		targetRef:   target,
	}

	// create incoming and outcoming flows it they aren't existed yet
	if fn.outcoming == nil {
		fn.outcoming = make([]*SequenceFlow, 0)
	}

	if target.incoming == nil {
		target.incoming = make([]*SequenceFlow, 0)
	}

	fn.outcoming = append(fn.outcoming, sf)
	target.incoming = append(target.incoming, sf)

	return sf, nil
}

// HasIncoming checks if the FlowNode has incoming flows.
func (fn *FlowNode) HasIncoming() bool {

	return len(fn.incoming) != 0
}

// ClearFlows clears incoming and outcoming flows for the FlowNode
func (fn *FlowNode) ClearFlows() {

	fn.incoming = []*SequenceFlow{}
	fn.outcoming = []*SequenceFlow{}
}
