package flow

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// *****************************************************************************

// Element is the abstract super class for all elements that can appear in
// a Process flow, which are FlowNodes, which consist of Activities,
// Choreography Activities, Gateways, and Events, Data Objects, Data
// Associations, and Sequence Flows.
type Element struct {
	foundation.BaseElement

	// The descriptive name of the element.
	name string

	// A reference to the Category Values that are associated with this Flow
	// Element.
	// NOTE: Since the CategoryValues is used only for visually grouping
	//       Elements visually in Group and to eleminate ciclyc imports
	//       bidirectional link between FlowElement and CategoryValue
	//       updated to uni-directional link from CategoryValue to FlowElement.
	// Categories []*artifacts.CategoryValue

	// Container consisted the element.
	container *ElementsContainer
}

// NewElement creates a new FlowElement and returns its pointer.
func NewElement(id, name string,
	docs ...*foundation.Documentation,
) *Element {
	return &Element{
		BaseElement: *foundation.NewBaseElement(id, docs...),
		name:        name,
	}
}

// Name returns the Element name.
func (fe *Element) Name() string {
	return fe.name
}

// Container returns the element hosted container if presented.
func (fe *Element) Container() *ElementsContainer {
	return fe.container
}

// *****************************************************************************

// ElementsContainer is an abstract super class for BPMN diagrams (or
// views) and defines the superset of elements that are contained in those
// diagrams. Basically, a ElementsContainer contains FlowElements, which
// are Events, Gateways, Sequence Flows, Activities, and Choreography
// Activities.
//
// There are four (4) types of ElementsContainers: Process, Sub-Process,
// Choreography, and Sub-Choreography.
type ElementsContainer struct {
	// Despite the standard stands for ElementContainer is based on
	// BaseElement gobpm removes this to avoid conflicts on Process creation
	// which inherits both CallableElement and ElementContainer
	// foundation.BaseElement

	elements map[string]*Element
}

// NewContainer creates an empty container and returns its pointer.
func NewContainer() *ElementsContainer {
	return &ElementsContainer{
		elements: map[string]*Element{},
	}
}

// Add adds the new element to the container if there is no
// duplication in id.
// If the container already consists of the Element,
// no error returned.
func (fec *ElementsContainer) Add(fe *Element) error {
	if fe == nil {
		return errs.ErrEmptyObject
	}

	// if container already consists of the Element
	// return OK.
	if fe.container == fec {
		return nil
	}

	if _, ok := fec.elements[fe.name]; ok {
		return errs.OperationFailed(errs.ErrNotFound, fe.name)
	}

	fec.elements[fe.name] = fe
	fe.container = fec

	return nil
}

// Remove removes element from contanier if found.
func (fec *ElementsContainer) Remove(id string) error {
	fe, ok := fec.elements[id]
	if !ok {
		return errs.OperationFailed(errs.ErrNotFound, id)
	}

	fe.container = nil
	delete(fec.elements, id)

	return nil
}

// Contains checks if container contains element with elementId.
func (fec *ElementsContainer) Contains(elementId string) bool {
	_, ok := fec.elements[elementId]

	return ok
}

// Elements returns a list of container elements.
func (fec *ElementsContainer) Elements() []*Element {
	ee := make([]*Element, len(fec.elements))

	i := 0
	for _, v := range fec.elements {
		ee[i] = v
		i++
	}

	return ee
}
