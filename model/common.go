package model

import (
	"fmt"
	"io"

	"github.com/dr-dobermann/gobpm/ctr"
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

func (id Id) String() string {
	return uuid.UUID(id).String()
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

type NamedVersionedElement struct {
	NamedElement
	version string
}

func (nve NamedVersionedElement) Version() string {
	return nve.version
}

type ItemKind uint8

const (
	Information ItemKind = iota
	Physical
)

type Import struct {
	impType   string
	location  string
	namespace string
}

type ItemDefinition struct {
	BaseElement
	itemKind     ItemKind
	structure    interface{}
	importRef    *Import
	isCollection bool
}

type Error struct {
	NamedElement
	errorCode string
	structure ItemDefinition
}

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
	audit       *ctr.Audit
	monitor     *ctr.Monitor
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
	BindToProcess(p *Process, laneName string)
	// ConnectFlow connects SequenceFlow to incoming or outcoming
	// slot of Node.
	// if se is SeSource then Node is the source end of the sequence,
	// else the Node is the target of the sequence
	ConnectFlow(sf *SequenceFlow, se SequenceEnd) error
	HasIncoming() bool
}

// base for Activities, Gates and Events
type FlowNode struct {
	FlowElement
	process   *Process
	lane      *Lane
	incoming  []*SequenceFlow
	outcoming []*SequenceFlow
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

func (fn *FlowNode) BindToLane(lane *Lane) error {
	if lane == nil {
		return NewModelError(nil, "lane name shouldn't be empty for task "+fn.name)
	}

	fn.process = lane.process
	fn.lane = lane

	return nil
}

func (fn *FlowNode) ConnectFlow(sf *SequenceFlow, se SequenceEnd) error {

	if sf == nil {
		return NewProcessModelError(fn.process.id,
			fmt.Sprintf("couldn't bind nil flow to no node %s", fn.name),
			nil)
	}

	// Node fn is the source of the sequence flow sf
	if se == SeSource {
		if fn.outcoming == nil {
			fn.outcoming = make([]*SequenceFlow, 0)
		}
		// check for correctness
		if sf.sourceRef.ID() != fn.id {
			return NewProcessModelError(fn.process.id,
				fmt.Sprintf("invalid connection. Node %v "+
					"should be the source of the flow %v",
					fn.id, sf.id),
				nil)
		}
		// check for duplicates
		for _, f := range fn.outcoming {
			if f.targetRef.ID() == sf.targetRef.ID() {
				return NewProcessModelError(fn.process.id,
					fmt.Sprintf("sequence flow %v already "+
						"connected to node %v",
						sf.id, fn.id),
					nil)
			}
		}

		fn.outcoming = append(fn.outcoming, sf)

	} else { // Node fn is the target of sequence flow sf
		if fn.incoming == nil {
			fn.incoming = make([]*SequenceFlow, 0)
		}

		if sf.targetRef.ID() != fn.id {
			return NewProcessModelError(fn.process.id,
				fmt.Sprintf("Node %v should be the target "+
					"of the sequence flow %v", fn.id, sf.id),
				nil)
		}

		for _, f := range fn.incoming {
			if f.sourceRef.ID() == sf.sourceRef.ID() {
				return NewProcessModelError(fn.process.id,
					fmt.Sprintf("sequence flow %v already connected to "+
						"Node %v", sf.id, fn.id),
					nil)
			}
		}

		fn.incoming = append(fn.incoming, sf)

	}

	return nil
}

func (fn *FlowNode) HasIncoming() bool {

	return len(fn.incoming) != 0
}

type SequenceEnd uint8

const (
	SeSource SequenceEnd = iota
	SeTarget
)

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

type CallableElement struct {
	NamedElement
	interfaces []*Interface
	ioSpec     InputOutputSpecification
	ioBinds    []InputOutputBinding
}

type TokenState uint16

const (
	TSLive TokenState = iota
	TSInactive
)

type Token struct {
	id    Id
	pID   Id
	state TokenState
	prevs []*Token
	nexts []*Token
}

func (t Token) ID() Id {
	return t.id
}

func (t Token) State() TokenState {
	return t.state
}

// NewToken creates a new Token with id tID, linked to
// process pID and which has parent p.
// tID could be uuid.Nil, then token id would be autogenerated
// p could be nil so the Token doesn't have any parent.
// If pID is uuid.Nil the nil will be return, since
// there is no reason to have Token without the process.
func NewToken(tID Id, pID Id) *Token {
	if pID == Id(uuid.Nil) {
		return nil
	}

	if tID == Id(uuid.Nil) {
		tID = NewID()
	}

	return &Token{tID, pID, TSLive, []*Token{}, []*Token{}}
}

// Split token onto n new tokens.
// Current token becomes inactive.
// if n == 0 the token itself will be returned with
// Live state.
// Token with inactive status couldn't be splitted and
// Split panics on this enquery
func (t *Token) Split(n uint16) []*Token {
	if t.State() == TSInactive {
		panic("Couldn't split inactive token " + t.ID().String())
	}

	tt := []*Token{}

	if n == 0 {
		return append(tt, t)
	}

	for i := 0; i < int(n); i++ {
		nt := &Token{NewID(), t.pID, TSLive, append([]*Token{}, t), []*Token{}}
		tt = append(tt, nt)
		t.nexts = append(t.nexts, nt)
	}
	t.state = TSInactive

	return tt
}

// func (t *Token) GetPrevious() []*Token {
// 	tt := make([]*Token, len(t.prevs))

// 	copy(tt, t.prevs)

// 	return tt
// }

// Join joins one token to another and returns the first one.
// Only tokens with Live status could be joined.
// if t is Inactive panic will fired.
// if jt is Inactive nothing will be joined
func (t *Token) Join(jt *Token) *Token {
	if t.state == TSInactive {
		panic("Couldn't joint to inactive token")
	}

	if jt.state == TSInactive {
		return t
	}

	jt.nexts = append(t.nexts, t)
	t.prevs = append(t.prevs, jt)
	jt.state = TSInactive

	return t
}

type Persister interface {
	io.Reader
	io.Writer
}
