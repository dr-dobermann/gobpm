package flow

import (
	"errors"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type ElementType string

const (
	NodeElement         ElementType = "Node"
	SequenceFlowElement ElementType = "SequenceFlow"
)

// Validate checks if t belongs to ElementType.
func (t ElementType) Validate() error {
	if t != NodeElement && t != SequenceFlowElement {
		return errs.New(
			errs.M("invalid ElementType: %q", t),
			errs.C(errorClass, errs.TypeCastingError))
	}

	return nil
}

// FlowElement is a common interface for all Element's childs.
type FlowElement interface {
	// ElementType returns type of the element.
	ElementType() ElementType

	// GetElement returns underlaying Element.
	GetElement() *Element
}

// Container interface provide ability for container to
// add a new element into itself.
type Container interface {
	// Add adds the e Element into itself or returns error on failure.
	Add(ElementType) error
}

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
	// DEV_NOTE: Since the CategoryValues is used only for visually grouping
	//       	 Elements visually in Group and to eleminate ciclyc imports
	//           bidirectional link between FlowElement and CategoryValue
	//           updated to uni-directional link from CategoryValue to
	//           FlowElement.
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
			name:        name,
		},
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

	// elements are indexed by its Ids.
	elements map[string]*Element
}

// --------------------- Container interface -----------------------------------

// Add adds non-empty unique Element e to the ElementContainer c.
// if e is empty, c already has e, c doesn't properly created or e belongs to
// other container, error occurred.
func (c *ElementsContainer) Add(fe FlowElement) error {
	if c.elements == nil {
		return errs.New(
			errs.M("containter doesn't created properly (use NewContainer)"),
			errs.C(errorClass, errs.InvalidObject))
	}

	if fe == nil {
		return errs.New(
			errs.M("element couldn't be empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	e := fe.GetElement()

	if e.container != nil {
		if e.container == c {
			return errs.New(
				errs.M("container already has element %q(%s)", e.name, e.Id()),
				errs.C(errorClass, errs.DuplicateObject))
		}

		return errs.New(
			errs.M("element %q(%s) belongs to another container %s",
				e.name, e.Id(), e.container.Id()),
			errs.C(errorClass, errs.InvalidParameter))
	}

	c.elements[e.Id()] = e
	e.container = c

	return nil
}

// NewContainer creates an empty container and returns its pointer on success or
// error on failure.
func NewContainer(
	baseOpts ...options.Option,
) (*ElementsContainer, error) {
	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &ElementsContainer{
			BaseElement: *be,
			elements:    map[string]*Element{},
		},
		nil
}

// AddElements adds the new element to the container.
// It adds only non-nil elements and returns the counter of added elements.
func (fec *ElementsContainer) AddElements(fee ...FlowElement) (int, error) {
	if fec.elements == nil {
		return 0, errs.New(
			errs.M("containter doesn't created properly (use NewContainer)"),
			errs.C(errorClass, errs.InvalidObject))
	}

	n := 0
	ee := []error{}
	for _, fe := range fee {
		if err := fec.Add(fe); err != nil {
			ee = append(ee, err)
			continue
		}

		n++
	}

	if len(ee) != 0 {
		return n, errors.Join(ee...)
	}

	return n, nil
}

// Remove removes elements from contanier if found and returns the number of
// removed elements.
func (fec *ElementsContainer) RemoveById(id string) error {
	if fec.elements == nil {
		return errs.New(
			errs.M("containter doesn't created properly (use NewContainer)"),
			errs.C(errorClass, errs.InvalidObject))
	}

	id = strings.Trim(id, " ")
	if id == "" {
		return errs.New(
			errs.M("empty id isn't allowed"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	fe, ok := fec.elements[id]
	if !ok {
		return errs.New(
			errs.M("element %s doesn't exists in the container", id),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	fe.container = nil
	delete(fec.elements, id)

	return nil
}

// Contains checks if container contains element with elementId.
func (fec *ElementsContainer) Contains(elementId string) bool {
	if fec.elements == nil {
		errs.Panic("containter doesn't created properly (use NewContainer)")

		return false
	}

	_, ok := fec.elements[elementId]

	return ok
}

// Elements returns a list of container elements.
func (fec *ElementsContainer) Elements() []*Element {
	if fec.elements == nil {
		errs.Panic("containter doesn't created properly (use NewContainer)")

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
