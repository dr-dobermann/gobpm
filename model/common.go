package model

import (
	"context"

	"github.com/dr-dobermann/gobpm/ctr"
	"github.com/google/uuid"
)

type Documentation struct {
	Text   string
	Format string
}

type Id uuid.UUID

type BaseElement struct {
	id Id
	Documentation
}

func (be BaseElement) ID() Id {
	return be.id
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
	BaseElement
	name      string
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
)

// base for FlowNode(Activities, Events, Gates), Data Objects, Data Associations
// and SequenceFlow
type FlowElement struct {
	BaseElement
	name        string
	audit       *ctr.Audit
	monitor     *ctr.Monitor
	elementType FlowElementType
}

func (fe FlowElement) Type() FlowElementType {
	return fe.elementType
}

func (fe FlowElement) Name() string {
	return fe.name
}

// base for Activities, Gates and Events
type FlowNode struct {
	FlowElement
	incoming  []SequenceFlow
	outcoming []SequenceFlow
}

// base for Process, Sub-Process, Choreography and Sub-Choreography
type FlowElementsContainer struct {
	FlowElement
	elements []FlowElement
}

type SequenceFlow struct {
	FlowElement
	Expression // Expression determines the possibility of
	// using path over this SequenceFlow.
	// Could be empty. If not, the path
	// couldn't start from Parallel Gate or
	// Event FloatNode
	sourceRef Id
	targetRef Id
}

type CallableElement struct {
	BaseElement
	name       string
	interfaces []*Interface
	ioSpec     InputOutputSpecification
	ioBinds    []InputOutputBinding
}

func (ce CallableElement) Name() string {
	return ce.name
}

type TokenState uint16

const (
	TSLive TokenState = iota
	TSEnded
)

type Token struct {
	id      Id
	m       *Model
	state   TokenState
	parents []*Token
}

func (t Token) ID() Id {
	return t.id
}

func (t *Token) Split(n uint16) []Token {
	tt := []Token{}

	for i := 0; i < int(n); i++ {
		tt = append(tt,
			Token{Id(uuid.New()), t.m, TSLive, append(t.parents, t)})
	}

	return tt
}

func (t *Token) Join(jt *Token) *Token {
	t.parents = append(t.parents, jt)
	jt.state = TSEnded

	return t
}

type Node interface {
	ProcessToken(ctx context.Context, t Token) error
	Link(to Node) error
	IsEqual(n Node) bool
}
