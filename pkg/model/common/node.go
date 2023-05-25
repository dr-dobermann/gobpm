package common

import (
	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/dr-dobermann/gobpm/pkg/model"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
)

type Node interface {
	ID() identity.Id
	Name() string
	Type() FlowElementType
	GetFlowNode() *FlowNode

	HasIncoming() bool

	// Copy creates a copy of node with
	// new ID, same name and with no incoming and outcoming flows
	Copy() Node

	// BindToProcess binds Node to a process id if it isn't already binded
	// ot another process and if pid isn't empty Id.
	BindToProcess(pid identity.Id) error

	GetProcessID() identity.Id
}

// base for Activities, Gates and Events
type FlowNode struct {
	FlowElement

	processID identity.Id

	incoming  []*SequenceFlow
	outcoming []*SequenceFlow
}

// GetFlowNode returns laying underneath FlowNode object.
func (fn *FlowNode) GetFlowNode() *FlowNode {

	return fn
}

// GetOutputFlows returns node's output flows
func (fn *FlowNode) GetOutputFlows() []*SequenceFlow {

	res := make([]*SequenceFlow, 0)
	if fn.outcoming != nil {
		res = append(res, fn.outcoming...)
	}

	return res
}

// BindToProcess binds FlowNode to the given process ID
// if it's still unbinded to any process.
func (fn *FlowNode) BindToProcess(pid identity.Id) error {

	if fn.processID != identity.EmptyID() {
		return model.NewModelError(fn.name, fn.ID(), nil,
			"node already binded to process %v", fn.processID)
	}

	if pid == identity.EmptyID() {
		return model.NewModelError(fn.name, fn.ID(), nil,
			"couldn bind to an empty process id")
	}

	fn.processID = pid

	return nil
}

func (fn FlowNode) GetProcessID() identity.Id {

	return fn.processID
}

// Connect connects fn with target FlowNode and return new SequenceFlow
// named flowName if there is no duplication.
// Connected nodes should be binded to the same process.
func (fn *FlowNode) Connect(target *FlowNode,
	flowName string, cond expression.Condition) (*SequenceFlow, error) {

	if target == nil {
		return nil,
			model.NewModelError(fn.name, fn.ID(), nil,
				"couldn't connect with nil node")
	}

	if fn.processID != target.processID || fn.processID == identity.EmptyID() {
		return nil,
			model.NewModelError(fn.name, fn.ID(), nil,
				"nodes should be binded to the same process. Have %v and %v",
				fn.processID, target.processID)
	}

	// check for duplicates
	for _, f := range fn.outcoming {
		if f.targetRef.ID() == target.ID() {
			return nil,
				model.NewModelError(fn.name, fn.ID(), nil,
					"node already connected to node %s[%v] via %s[%v]",
					target.name, target.ID(), f.name, f.ID())
		}
	}

	sf := &SequenceFlow{
		FlowElement: *NewElement(identity.EmptyID(), flowName, EtSequenceFlow),
		condition:   cond,
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
func (fn FlowNode) HasIncoming() bool {

	return len(fn.incoming) != 0
}

// Copy creates a copy of a FlowNode with no
// incoming or outcoming flows
func (fn *FlowNode) Copy() Node {
	cfn := FlowNode{
		FlowElement: *NewElement(identity.EmptyID(), fn.name, fn.elementType),
		incoming:    []*SequenceFlow{},
		outcoming:   []*SequenceFlow{},
	}

	return &cfn
}
