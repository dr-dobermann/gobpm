package common

import (
	"fmt"

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

// -------------------- CallableElement ----------------------------------------
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

// ------------------ FlowElement ----------------------------------------------
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
	EtSequenceFlow
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
		"SequenceFlow",
	}[fet]
}

// base for FlowNode(Activities, Events, Gates), Data Objects, Data Associations
// and SequenceFlow
type FlowElement struct {
	NamedElement
	// audit       *ctr.Audit
	// monitor     *ctr.Monitor
	category    string
	elementType FlowElementType
	container   *FlowElementContainer
}

func (fe *FlowElement) Type() FlowElementType {
	return fe.elementType
}

func NewElement(id identity.Id, name string, tp FlowElementType) *FlowElement {

	if tp == EtUnspecified {
		panic(fmt.Sprintf("type should be specified for element %q(%q)", name, id.String()))
	}

	return &FlowElement{
		NamedElement: *NewNamedElement(id, name),
		elementType:  tp}
}

func (fe *FlowElement) SetType(et FlowElementType) {

	fe.elementType = et
}

func (fe *FlowElement) Container() *FlowElementContainer {

	return fe.container
}

func (fe *FlowElement) AssignTo(fec *FlowElementContainer) error {

	if fe.container != nil {
		if fe.container == fec {
			return nil
		}

		return fmt.Errorf("element %q(%q) already assigned to container %q(%q)",
			fe.name, fe.ID(), fec.name, fe.container.ID())
	}

	fe.container = fec

	return nil
}

func (fe *FlowElement) UnassignFrom(fec *FlowElementContainer) error {

	if fe.container != fec {
		return fmt.Errorf("element %q(%q) isn't assigned to container %q(%q)",
			fe.name, fe.ID(), fec.name, fec.ID())
	}

	fe.container = nil

	return nil
}

func (fe *FlowElement) Category() string {

	return fe.category
}

func (fe *FlowElement) SetCategory(c string) {

	fe.category = c
}

// ------------------ FlowElementContainer -------------------------------------
type FlowElementContainer struct {
	NamedElement

	laneSets []LaneSet

	elements map[identity.Id]*FlowElement
}

func NewContainer(id identity.Id, name string) *FlowElementContainer {

	return &FlowElementContainer{
		NamedElement: *NewNamedElement(id, name),
		elements:     make(map[identity.Id]*FlowElement),
	}
}

func (fec *FlowElementContainer) Add(el ...*FlowElement) error {

	for _, e := range el {

		if e.container == fec {
			continue
		}

		if e.Container() != nil {
			return fmt.Errorf("element %q(%q) already assigned to container %q(%q)",
				e.name, e.ID(), e.Container().name, e.Container().ID())
		}

		if _, ok := fec.elements[e.ID()]; ok {
			return fmt.Errorf("element %q(%q) already existed in container %q(%q)",
				e.name, e.ID(), fec.name, fec.ID())
		}

		e.AssignTo(fec)
		fec.elements[e.ID()] = e
	}

	return nil
}

func (fec *FlowElementContainer) Get(id identity.Id) (*FlowElement, error) {

	e, ok := fec.elements[id]
	if !ok {
		return nil,
			fmt.Errorf("there is no element %q in container %q(%q)",
				id, fec.name, fec.ID())
	}

	return e, nil
}

func (fec *FlowElementContainer) GetAll() []*FlowElement {
	res := make([]*FlowElement, 0)

	for _, e := range fec.elements {
		res = append(res, e)
	}

	return res
}

func (fec *FlowElementContainer) Remove(id identity.Id) error {

	fe, ok := fec.elements[id]
	if !ok {
		return fmt.Errorf("element %q isn't existed in container %q(%q)",
			id, fec.name, fec.ID())
	}

	fe.UnassignFrom(fec)

	delete(fec.elements, id)

	return nil
}
