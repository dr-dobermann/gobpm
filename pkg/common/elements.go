package common

import (
	"github.com/dr-dobermann/gobpm/pkg/foundation"
	"github.com/dr-dobermann/gobpm/pkg/identity"
)

// ------------------- Named --------------------------------------------------
type NamedElement struct {
	foundation.BaseElement

	name string
}

func (ne *NamedElement) Name() string {
	return ne.name
}

func (ne *NamedElement) SetName(name string) {
	ne.name = name
}

// -------------------- Callable ----------------------------------------------
type CallableElement struct {
	NamedElement
	// interfaces []*Interface
	// ioSpec     InputOutputSpecification
	// ioBinds    []InputOutputBinding
}

func NewNamedElement(id identity.Id, name string) *NamedElement {
	return &NamedElement{BaseElement: *foundation.New(id),
		name: name}
}

// ------------------ Flow ----------------------------------------------------
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

func (fe *FlowElement) Type() FlowElementType {
	return fe.elementType
}

func NewFlowElement(id identity.Id, name string, tp FlowElementType) *FlowElement {
	return &FlowElement{
		NamedElement: *NewNamedElement(id, name),
		elementType:  tp}
}

func (fe *FlowElement) SetType(et FlowElementType) {
	fe.elementType = et
}
