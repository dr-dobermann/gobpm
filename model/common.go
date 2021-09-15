package model

import "github.com/dr-dobermann/gobpm/ctr"

type Documentation struct {
	text   string
	format string
}

type id uint64

type BaseElement struct {
	ID id
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
	name    string
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
	sourceRef id
	targetRef id
}
<<<<<<< HEAD

type CallableElement struct {
	BaseElement
	name            string
	ioSpecification InputOutputSpecification
}
=======
>>>>>>> cd1bb6ab4d496deef6cc2b2baa563bdcafa033d0
