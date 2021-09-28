package model

import "github.com/dr-dobermann/gobpm/ctr"

type Documentation struct {
	Text   string
	Format string
}

type Id uint64

type BaseElement struct {
	ID Id
	Documentation
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

// base for FlowNode(Activities, Events, Gates), Data Objects, Data Associations
// and SequenceFlow
type FlowElement struct {
	BaseElement
	Name    string
	audit   *ctr.Audit
	monitor *ctr.Monitor
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

type FlowDirection uint8

const (
	None FlowDirection = iota
	Begin
	End
	Both
)

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
	Name            string
	interfaces      []*Interface
	ioSpecification InputOutputSpecification
	ioBindings      []InputOutputBinding
}
