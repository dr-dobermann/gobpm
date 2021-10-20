package model

import (
	"context"
	"fmt"
	"io"

	"github.com/dr-dobermann/gobpm/ctr"
	"github.com/google/uuid"
)

type Documentation struct {
	Text   string
	Format string
}

type Id uuid.UUID

func NewID() Id {
	return Id(uuid.New())
}

func (id Id) String() string {
	return fmt.Sprint(uuid.UUID(id))
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
	EtActivity FlowElementType = iota
	EtEvent
	EtGate
	EtDataObject
	EtDataAssociation
	EtContainer
	EtProcess
)

// base for FlowNode(Activities, Events, Gates), Data Objects, Data Associations
// and SequenceFlow
type FlowElement struct {
	NamedElement
	container   *FlowElementsContainer
	audit       *ctr.Audit
	monitor     *ctr.Monitor
	elementType FlowElementType
}

func (fe FlowElement) Type() FlowElementType {
	return fe.elementType
}

// base for Activities, Gates and Events
type FlowNode struct {
	FlowElement
	incoming  []*SequenceFlow
	outcoming []*SequenceFlow
}

// base for Process, Sub-Process, Choreography and Sub-Choreography
type FlowElementsContainer struct {
	FlowElement
	containers []*FlowElementsContainer
	elements   []*FlowElement
}

func (fec *FlowElementsContainer) InsertElement(fe *FlowElement) error {
	if fe == nil {
		return NewModelError(uuid.Nil, "Couldn't insert nil FlowElement into container", nil)
	}

	for _, e := range fec.elements {
		if e.id == fe.id {
			return NewModelError(uuid.Nil,
				"Element "+fe.id.String()+" already exists in the contatiner "+fec.id.String(),
				nil)
		}
	}

	fec.elements = append(fec.elements, fe)
	fe.container = fec

	return nil
}

func (fec *FlowElementsContainer) Elements() []Id {
	fes := []Id{}

	for _, e := range fec.elements {
		fes = append(fes, e.id)
	}

	return fes
}

func (fec *FlowElementsContainer) RemoveElement(id Id) error {
	if id == Id(uuid.Nil) {
		return NewModelError(uuid.Nil, "Couldn't remove element with Nil id", nil)
	}

	var fe *FlowElement
	pos := -1
	for i, e := range fec.elements {
		if e.id == id {
			pos, fe = i, e
			break
		}
	}

	if pos == -1 {
		return NewModelError(uuid.Nil,
			"Element "+id.String()+" doesn't found in the container "+fec.id.String(),
			nil)
	}

	fe.container = nil
	fec.elements = append(fec.elements[:pos], fec.elements[pos+1:]...)

	return nil
}

type SequenceFlow struct {
	FlowElement
	// Expression determines the possibility of
	// using path over this SequenceFlow.
	// Could be empty. If not, the path
	// couldn't start from Parallel Gate or
	// Event FloatNode
	expr      *Expression
	sourceRef Id
	targetRef Id
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

	return &Token{tID, pID, TSLive, []*Token{}}
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
		tt = append(tt,
			&Token{NewID(), t.pID, TSLive, append([]*Token{}, t)})
	}
	t.state = TSInactive

	return tt
}

func (t *Token) GetPrevious() []*Token {
	tt := make([]*Token, len(t.prevs))

	copy(tt, t.prevs)

	return tt
}

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

	t.prevs = append(t.prevs, jt)
	jt.state = TSInactive

	return t
}

type Node interface {
	ID() Id
	ProcessToken(ctx context.Context, t Token) error
	// Link links one Node to another via SequenceFlow object.
	// Should check if the both Nodes related to the same Model
	Link(to Node) error
	IsEqual(n Node) bool
	PutIn(c *FlowElementsContainer) error
}

type Persister interface {
	io.Reader
	io.Writer
}
