package common

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/artifacts"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// *****************************************************************************

// FlowElement is the abstract super class for all elements that can appear in
// a Process flow, which are FlowNodes, which consist of Activities,
// Choreography Activities, Gateways, and Events, Data Objects, Data
// Associations, and Sequence Flows.
type FlowElement struct {
	foundation.BaseElement

	// The descriptive name of the element.
	name string

	// A reference to the Category Values that are associated with this Flow
	// Element.
	Categories []*artifacts.CategoryValue

	// Container consisted the element.
	container *FlowElementsContainer
}

// NewFlowElement creates a new FlowElement and returns its pointer.
func NewFlowElement(id, name string,
	docs ...*foundation.Documentation,
) *FlowElement {
	return &FlowElement{
		BaseElement: *foundation.NewBaseElement(id, docs...),
		name:        name,
	}
}

// Name returns the FlowElement name.
func (fe FlowElement) Name() string {
	return fe.name
}

// Container returns the element hosted container if presented.
func (fe FlowElement) Container() *FlowElementsContainer {
	return fe.container
}

// *****************************************************************************

// FlowElementsContainer is an abstract super class for BPMN diagrams (or
// views) and defines the superset of elements that are contained in those
// diagrams. Basically, a FlowElementsContainer contains FlowElements, which
// are Events, Gateways, Sequence Flows, Activities, and Choreography
// Activities.
//
// There are four (4) types of FlowElementsContainers: Process, Sub-Process,
// Choreography, and Sub-Choreography.
type FlowElementsContainer struct {
	// Despite the standard stands for FlowElementContainer is based on
	// BaseElement gobpm removes this to avoid conflicts on Process creation
	// which inherits both CallableElement and FlowElementContainer
	// foundation.BaseElement

	flowElements map[string]*FlowElement
}

// NewContainer creates an empty container and returns its pointer.
func NewContainer() *FlowElementsContainer {
	return &FlowElementsContainer{
		flowElements: map[string]*FlowElement{},
	}
}

// Add adds the new element to the container if there is no
// duplication in id.
// If the container already consists of the FlowElement,
// no error returned.
func (fec *FlowElementsContainer) Add(fe *FlowElement) error {
	if fe == nil {
		return fmt.Errorf("no element to add")
	}

	// if container already consists of the FlowElement
	// return OK.
	if fe.container == fec {
		return nil
	}

	if _, ok := fec.flowElements[fe.name]; ok {
		return fmt.Errorf("duplicate element id %q", fe.name)
	}

	fec.flowElements[fe.name] = fe
	fe.container = fec

	return nil
}

// Remove removes element from contanier if found.
func (fec *FlowElementsContainer) Remove(id string) error {
	fe, ok := fec.flowElements[id]
	if !ok {
		return fmt.Errorf("no element %q found", id)
	}

	fe.container = nil
	delete(fec.flowElements, id)

	return nil
}

// Contains checks if container contains element with elementId.
func (fec *FlowElementsContainer) Contains(elementId string) bool {
	_, ok := fec.flowElements[elementId]

	return ok
}

// Elements returns a list of container elements.
func (fec *FlowElementsContainer) Elements() []*FlowElement {
	ee := make([]*FlowElement, len(fec.flowElements))

	i := 0
	for _, v := range fec.flowElements {
		ee[i] = v
		i++
	}

	return ee
}
