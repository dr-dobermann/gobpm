package flow

import (
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
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

// NewElement creates a new FlowElement and returns its pointer on success or
// error on faailure.
func NewElement(
	name string,
	baseOpts ...options.Option,
) (*Element, error) {
	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &Element{
			BaseElement: *be,
			name:        name},
		nil
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
	foundation.BaseElement

	elements map[string]*Element
}

// NewContainer creates an empty container and returns its pointer.
func NewContainer(
	baseOpts ...options.Option,
) *ElementsContainer {
	return &ElementsContainer{
		BaseElement: *foundation.MustBaseElement(baseOpts...),
		elements:    map[string]*Element{},
	}
}

// Add adds the new element to the container.
// It adds only non-nil elements and returns the counter of added elements.
func (fec *ElementsContainer) Add(fee ...*Element) int {
	if fec.elements == nil {
		fec.elements = map[string]*Element{}
	}

	n := 0

	for _, fe := range fee {
		if fe == nil {
			continue
		}

		fec.elements[fe.name] = fe
		fe.container = fec

		n++
	}

	return n
}

// Remove removes elements from contanier if found and returns the number of
// removed elements.
func (fec *ElementsContainer) Remove(idd ...string) int {
	if fec.elements == nil {
		fec.elements = map[string]*Element{}

		return 0
	}

	n := 0
	for _, id := range idd {
		fe, ok := fec.elements[id]
		if !ok {
			continue
		}

		if _, ok := fec.elements[fe.Id()]; ok {
			fe.container = nil
			delete(fec.elements, id)

			n++
		}
	}

	return n
}

// Contains checks if container contains element with elementId.
func (fec *ElementsContainer) Contains(elementId string) bool {
	if fec.elements == nil {
		fec.elements = map[string]*Element{}

		return false
	}

	_, ok := fec.elements[elementId]

	return ok
}

// Elements returns a list of container elements.
func (fec *ElementsContainer) Elements() []*Element {
	if fec.elements == nil {
		fec.elements = map[string]*Element{}

		return []*Element{}
	}

	ee := make([]*Element, len(fec.elements))

	i := 0
	for _, v := range fec.elements {
		ee[i] = v
		i++
	}

	return ee
}
