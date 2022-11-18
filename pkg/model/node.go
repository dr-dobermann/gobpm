package model

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/common"
	mid "github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/google/uuid"
)

type Node interface {
	ID() mid.Id
	Name() string
	Type() common.FlowElementType
	LaneName() string
	ProcessID() mid.Id
	PutOnLane(lane *Lane) error
	// ConnectFlow connects SequenceFlow to incoming or outcoming
	// slot of Node.
	// if se is SeSource then Node is the source end of the sequence,
	// else the Node is the target of the sequence
	ConnectFlow(sf *SequenceFlow, se SequenceEnd) error
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
	common.FlowElement
	process   *Process
	lane      *Lane
	incoming  []*SequenceFlow
	outcoming []*SequenceFlow

	// that one will receive a token when none of the
	// outcoming flows is true
	// if EmptyID there is no default flow
	defaultFlowID mid.Id
}

func (fn *FlowNode) LaneName() string {
	return fn.lane.Name()
}

func (fn *FlowNode) ProcessID() mid.Id {

	if fn.process == nil {
		return mid.Id(uuid.Nil)
	}

	return fn.process.ID()
}

// returns flow node's output
func (fn *FlowNode) GetOutputFlows() []*SequenceFlow {
	return append([]*SequenceFlow{}, fn.outcoming...)
}

func (fn *FlowNode) PutOnLane(lane *Lane) error {
	if lane == nil {
		return NewModelError(nil, "lane name shouldn't be empty for task "+fn.Name())
	}

	fn.process = lane.process
	fn.lane = lane

	return nil
}

func (fn *FlowNode) SetDefaultFlow(id mid.Id) error {
	if id == mid.EmptyID() {
		return fmt.Errorf("couldn't make nil flow as default")
	}

	var flow *SequenceFlow

	for i, f := range fn.outcoming {
		if f.ID() == id {
			flow = fn.outcoming[i]
			break
		}
	}

	if flow.GetTarget().ID() == fn.ID() {
		return fmt.Errorf("couldn't make default flow on itself")
	}

	if flow == nil {
		return fmt.Errorf("Id %v doesn't existed in outgoing flows", id)
	}

	fn.defaultFlowID = flow.ID()

	return nil
}

// connects fn over the sf to another FlowNode using fn as a se sequence end.
func (fn *FlowNode) ConnectFlow(sf *SequenceFlow, se SequenceEnd) error {
	if sf == nil {
		return NewPMErr(fn.process.ID(), nil,
			"couldn't bind nil flow to no node '%s'", fn.Name())
	}

	// create incoming and outcoming flows it they aren't existed yet
	if fn.outcoming == nil {
		fn.outcoming = make([]*SequenceFlow, 0)
	}
	if fn.incoming == nil {
		fn.incoming = make([]*SequenceFlow, 0)
	}

	// check for correctness
	if (se == SeSource && sf.sourceRef.ID() != fn.ID()) ||
		(se == SeTarget && sf.targetRef.ID() != fn.ID()) {
		return NewPMErr(fn.process.ID(), nil,
			"connection failed for Flow [%v] end [%s] "+
				"node ID [%v], src ID [%v], trg ID [%v]",
			sf.ID(), se.String(),
			fn.ID(), sf.sourceRef.ID(), sf.targetRef.ID())
	}

	flow := fn.outcoming // by default assumes seSource flow end
	if se == SeTarget {
		flow = fn.incoming
	}

	// check for duplicates
	for _, f := range flow {
		if (se == SeSource && f.targetRef.ID() == sf.targetRef.ID()) ||
			(se == SeTarget && f.sourceRef.ID() == sf.sourceRef.ID()) {
			return NewPMErr(fn.process.ID(), nil,
				"sequence flow %v[%s] already "+
					"connected to node %v",
				sf.ID(), se.String(), fn.ID())
		}
	}

	if se == SeSource {
		fn.outcoming = append(fn.outcoming, sf)
	} else {
		fn.incoming = append(fn.incoming, sf)
	}

	return nil
}

func (fn *FlowNode) HasIncoming() bool {

	return len(fn.incoming) != 0
}

func (fn *FlowNode) ClearFlows() {

	fn.incoming = []*SequenceFlow{}
	fn.outcoming = []*SequenceFlow{}
}
