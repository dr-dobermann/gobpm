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

// ElementType represents different types of BPMN flow elements.
type ElementType string

const (
	// InvalidElement represents an invalid element type.
	InvalidElement ElementType = "INVALID_ELEMENT_TYPE"
	// DataObjectElement represents a data object element type.
	DataObjectElement ElementType = "DataObject"
	// NodeElement represents a node element type.
	NodeElement ElementType = "Node"
	// SequenceBaseElement represents a sequence flow element type.
	SequenceBaseElement ElementType = "SequenceFlow"
)

// Validate checks if t belongs to ElementType.
func (t ElementType) Validate() error {
	if t != NodeElement && t != SequenceBaseElement {
		return errs.New(
			errs.M("invalid ElementType: %q", t),
			errs.C(errorClass, errs.TypeCastingError))
	}

	return nil
}

// Element is the abstract super class for all elements that can appear in
// a Process flow, which are BaseNodes, which consist of Activities,
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
	//           bidirectional link between BaseElement and CategoryValue
	//           updated to uni-directional link from CategoryValue to
	//           BaseElement.
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

// Container is an abstract super class for BPMN diagrams (or
// views) and defines the superset of elements that are contained in those
// diagrams. Basically, a ElementsContainer contains BaseElements, which
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
//                               BaseElement
// ============================================================================

// BaseElement is a base class for all flowing elements.
type BaseElement struct {
	foundation.BaseElement

	name      string
	container Container
}

// NewBaseElement creates a new BaseElement with the given name and options.
func NewBaseElement(name string, opts ...options.Option) (*BaseElement, error) {
	be, err := foundation.NewBaseElement(opts...)
	if err != nil {
		return nil,
			fmt.Errorf("BaseElement building failed: %w", err)
	}

	return &BaseElement{
			BaseElement: *be,
			name:        name,
		},
		nil
}

// Name returns the name of the BaseElement.
func (fe *BaseElement) Name() string {
	return fe.name
}

// BindTo adds SequenceFlow sf into Container c
func (fe *BaseElement) BindTo(c Container) error {
	if c == nil {
		return errs.New(
			errs.M("container couldn't be empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if fe.container != nil {
		if fe.container != c {
			return errs.New(
				errs.M("flow already belongs to container %s", c.ID()),
				errs.C(errorClass, errs.InvalidParameter))
		}
	}

	fe.container = c

	return nil
}

// Unbind unbinds SequenceFlow from current container.
// if sf isn't binded to any container, error will be returned.
func (fe *BaseElement) Unbind() error {
	if fe.container == nil {
		return errs.New(
			errs.M("flow doesn't belong to any container"),
			errs.C(errorClass, errs.InvalidObject))
	}

	fe.container = nil

	return nil
}

// Container returns pointer on container which consists of sf.
func (fe *BaseElement) Container() Container {
	return fe.container
}

// EType returns invalid element type for generic BaseElement.
// Every Element should implement itsown EType.
func (fe *BaseElement) EType() ElementType {
	errs.Panic("couldn't use Type for generic BaseElement")

	return InvalidElement
}

// ----------------------------------------------------------------------------

// check interfaces
var (
	_ Element = (*BaseElement)(nil)
)
