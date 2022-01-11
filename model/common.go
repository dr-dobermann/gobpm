package model

import (
	"fmt"

	"github.com/google/uuid"
)

type Documentation struct {
	Text   []byte
	Format string
}

type Id uuid.UUID

func NewID() Id {
	return Id(uuid.New())
}

func EmptyID() Id {
	return Id(uuid.Nil)
}

func (id Id) String() string {
	return uuid.UUID(id).String()
}

func (id Id) GetLast(n int) string {
	s := id.String()
	if n > len(s) {
		n = len(s)
	}

	return s[len(s)-n:]
}

type BaseElement struct {
	id Id
	Documentation
}

func (be BaseElement) ID() Id {
	return be.id
}

type NamedElement struct {
	BaseElement
	name string
}

func (ne NamedElement) Name() string {
	return ne.name
}

type ItemKind uint8

const (
	Information ItemKind = iota
	Physical
)

type FlowElementType uint8

const (
	EtUnspecified FlowElementType = iota
	EtActivity
	EtEvent
	EtGateway
	EtDataObject
	EtDataAssociation
	EtProcess
	EtMessage
	EtLane
)

func (fet FlowElementType) String() string {
	return []string{
		"Unspecified",
		"Activity",
		"Event",
		"Gateway",
		"DataObject",
		"DataAssociation",
		"Process",
		"Message",
		"Lane",
	}[fet]
}

// base for FlowNode(Activities, Events, Gates), Data Objects, Data Associations
// and SequenceFlow
type FlowElement struct {
	NamedElement
	// audit       *ctr.Audit
	// monitor     *ctr.Monitor
	elementType FlowElementType
}

func (fe FlowElement) Type() FlowElementType {
	return fe.elementType
}

type Node interface {
	ID() Id
	Name() string
	Type() FlowElementType
	LaneName() string
	ProcessID() Id
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
	FlowElement
	process   *Process
	lane      *Lane
	incoming  []*SequenceFlow
	outcoming []*SequenceFlow

	// that one will receive a token when none of the
	// outcoming flows is true
	// if EmptyID there is no default flow
	defaultFlowID Id
}

func (fn *FlowNode) ID() Id {
	return fn.id
}

func (fn *FlowNode) LaneName() string {
	return fn.lane.name
}

func (fn *FlowNode) ProcessID() Id {

	if fn.process == nil {
		return Id(uuid.Nil)
	}

	return fn.process.id
}

// returns flow node's output
func (fn *FlowNode) GetOutputFlows() []*SequenceFlow {
	return append([]*SequenceFlow{}, fn.outcoming...)
}

func (fn *FlowNode) PutOnLane(lane *Lane) error {
	if lane == nil {
		return NewModelError(nil, "lane name shouldn't be empty for task "+fn.name)
	}

	fn.process = lane.process
	fn.lane = lane

	return nil
}

func (fn *FlowNode) SetDefaultFlow(id Id) error {
	if id == EmptyID() {
		return fmt.Errorf("couldn't make nil flow as default")
	}

	var flow *SequenceFlow

	for i, f := range fn.outcoming {
		if f.id == id {
			flow = fn.outcoming[i]
			break
		}
	}

	if flow.GetTarget().ID() == fn.id {
		return fmt.Errorf("couldn't make default flow on itself")
	}

	if flow == nil {
		return fmt.Errorf("Id %v doesn't existed in outgoing flows", id)
	}

	fn.defaultFlowID = flow.id

	return nil
}

func (fn *FlowNode) ConnectFlow(sf *SequenceFlow, se SequenceEnd) error {
	if sf == nil {
		return NewPMErr(fn.process.id, nil,
			"couldn't bind nil flow to no node '%s'", fn.name)
	}

	// create incoming and outcoming flows it they aren't existed yet
	if fn.outcoming == nil {
		fn.outcoming = make([]*SequenceFlow, 0)
	}
	if fn.incoming == nil {
		fn.incoming = make([]*SequenceFlow, 0)
	}

	// check for correctness
	if (se == SeSource && sf.sourceRef.ID() != fn.id) ||
		(se == SeTarget && sf.targetRef.ID() != fn.id) {
		return NewPMErr(fn.process.id, nil,
			"connection failed for Flow [%v] end [%s] "+
				"node ID [%v], src ID [%v], trg ID [%v]",
			sf.id, se.String(),
			fn.id, sf.sourceRef.ID(), sf.targetRef.ID())
	}

	// check for duplicates
	flow := fn.outcoming // by default assumes seSource flow end
	if se == SeTarget {
		flow = fn.incoming
	}

	for _, f := range flow {
		if (se == SeSource && f.targetRef.ID() == sf.targetRef.ID()) ||
			(se == SeTarget && f.sourceRef.ID() == sf.sourceRef.ID()) {
			return NewPMErr(fn.process.id, nil,
				"sequence flow %v[%s] already "+
					"connected to node %v",
				sf.id, se.String(), fn.id)
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

type SequenceEnd uint8

const (
	SeSource SequenceEnd = iota
	SeTarget
)

func (se SequenceEnd) String() string {
	return []string{"Source", "Target"}[se]
}

type SequenceFlow struct {
	FlowElement
	process *Process
	// Expression determines the possibility of
	// using path over this SequenceFlow.
	// Could be empty. If not, the path
	// couldn't start from Parallel Gate or
	// Event FloatNode
	expr      *Expression
	sourceRef Node
	targetRef Node
}

func (sf *SequenceFlow) GetTarget() Node {
	return sf.targetRef
}

func (sf *SequenceFlow) GetSource() Node {
	return sf.sourceRef
}

type CallableElement struct {
	NamedElement
	// interfaces []*Interface
	// ioSpec     InputOutputSpecification
	// ioBinds    []InputOutputBinding
}
