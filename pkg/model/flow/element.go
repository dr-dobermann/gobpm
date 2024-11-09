package flow

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// ============================================================================
//                               Element
// ============================================================================

type ElementType string

const (
	InvalidElement      ElementType = "INVALID_ELEMENT_TYPE"
	DataObjectElement   ElementType = "DataObject"
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

// Element is the abstract super class for all elements that can appear in
// a Process flow, which are FlowNodes, which consist of Activities,
// Choreography Activities, Gateways, and Events, Data Objects, Data
// Associations, and Sequence Flows.
type Element interface {
	foundation.BaseObject

	// The descriptive name of the element.
	foundation.Namer

	// A reference to the Category Values that are associated with this Flow
	// Element.
	// DEV_NOTE: Since the CategoryValues is used only for visually grouping
	//       	 Elements visually in Group and to eleminate ciclyc imports
	//           bidirectional link between FlowElement and CategoryValue
	//           updated to uni-directional link from CategoryValue to
	//           FlowElement.
	// Categories []*artifacts.CategoryValue

	// Container consisted the element.
	Container() Container

	EType() ElementType

	BindTo(Container) error
	Unbind() error
}

// ============================================================================
//                               Container
// ============================================================================

// ElementsContainer is an abstract super class for BPMN diagrams (or
// views) and defines the superset of elements that are contained in those
// diagrams. Basically, a ElementsContainer contains FlowElements, which
// are Events, Gateways, Sequence Flows, Activities, and Choreography
// Activities.
//
// There are four (4) types of ElementsContainers: Process, Sub-Process,
// Choreography, and Sub-Choreography.
type Container interface {
	foundation.BaseObject

	Add(Element) error
	Remove(Element) error

	Elements() []Element
}

// ============================================================================
//                               FlowElement
// ============================================================================

// FlowElement is a base class for all flowing elements.
type FlowElement struct {
	foundation.BaseElement

	name      string
	container Container
}

func NewFlowElement(name string, opts ...options.Option) (*FlowElement, error) {
	be, err := foundation.NewBaseElement(opts...)
	if err != nil {
		return nil,
			fmt.Errorf("BaseElement building failed: %w", err)
	}

	return &FlowElement{
			BaseElement: *be,
			name:        name,
		},
		nil
}

// -------------- Element interface --------------------------------------------
// Name returns the name of the FlowElement.
func (fe *FlowElement) Name() string {
	return fe.name
}

// BindTo adds SequenceFlow sf into Container c
func (fe *FlowElement) BindTo(c Container) error {
	if c == nil {
		return errs.New(
			errs.M("container couldn't be empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if fe.container != nil {
		if fe.container != c {
			return errs.New(
				errs.M("flow already belongs to container %s", c.Id()),
				errs.C(errorClass, errs.InvalidParameter))
		}
	}

	fe.container = c

	return nil
}

// Unbind unbinds SequenceFlow from current container.
// if sf isn't binded to any container, error will be returned.
func (fe *FlowElement) Unbind() error {
	if fe.container == nil {
		return errs.New(
			errs.M("flow doesn't belong to any container"),
			errs.C(errorClass, errs.InvalidObject))
	}

	fe.container = nil

	return nil
}

// Container returns pointer on container which consists of sf.
func (fe *FlowElement) Container() Container {
	return fe.container
}

// EType returns invalid element type for generic FlowElement.
// Every Element should implement itsown EType.
func (fe *FlowElement) EType() ElementType {
	errs.Panic("couldn't use Type for generic FlowElement")

	return InvalidElement
}

// ----------------------------------------------------------------------------

// check interfaces
var (
	_ Element = (*FlowElement)(nil)
)
