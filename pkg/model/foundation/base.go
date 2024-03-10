package foundation

import (
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/google/uuid"
)

const (
	defaultDocFormat = "text/plain"
)

type (
	Identifyer interface {
		Id() string
	}

	Documentator interface {
		Docs() []Documentation
	}

	Namer interface {
		Name() string
	}
)

// *****************************************************************************

// All BPMN elements that inherit from the BaseElement will have the capability,
// through the Documentation element, to have one (1) or more text descriptions
// of that element.
type Documentation struct {
	// This attribute is used to capture the text descriptions of a
	// BPMN element.
	text string

	// This attribute identifies the format of the text. It MUST follow
	// the mime-type format. The default is "text/plain".
	format string
}

// NewDoc creates new Documentation item. If no format is given,
// then text/plain is used.
func NewDoc(text, format string) *Documentation {
	if format == "" {
		format = defaultDocFormat
	}

	return &Documentation{
		text:   text,
		format: format,
	}
}

// Text returns Documentation text.
func (d Documentation) Text() string {
	return d.text
}

// Format returns Documentation format.
func (d Documentation) Format() string {
	return d.format
}

// *****************************************************************************

// BaseElement is the abstract super class for most BPMN elements.
// It provides the attributes id and docs, which other
// elements will inherit.
type BaseElement struct {
	// This attribute is used to uniquely identify BPMN elements. The id is
	// REQUIRED if this element is referenced or intended to be referenced by
	// something else. If the element is not currently referenced and is never
	// intended to be referenced, the id MAY be omitted.
	id string

	// This attribute is used to annotate the BPMN element, such as descriptions
	// and other documentation.
	docs []Documentation
}

// NewBaseElement creates a new BaseElement with given id
// if id is empty, then new UUID is generated.
func NewBaseElement(opts ...options.Option) (*BaseElement, error) {
	bc := baseConfig{
		id:   uuid.Must(uuid.NewRandom()).String(),
		docs: []Documentation{},
	}

	for _, opt := range opts {
		if err := opt.Apply(&bc); err != nil {
			return nil, err
		}
	}

	return bc.baseElement(), nil
}

// MustBaseElement tries to create a new BaseElement and returns its pointer
// on success or error on failure.
func MustBaseElement(opts ...options.Option) *BaseElement {
	be, err := NewBaseElement(opts...)
	if err != nil {
		panic(err.Error())
	}

	return be
}

// Id returns the BaseElement Id.
func (be BaseElement) Id() string {
	return be.id
}

// Docs returns the copy of BaseElement documentation.
func (be BaseElement) Docs() []Documentation {
	return append([]Documentation{}, be.docs...)
}

// Clone creates a clone of the BaseElement.
func (be BaseElement) Clone() *BaseElement {
	cbe := BaseElement{
		id:   be.id,
		docs: make([]Documentation, len(be.docs)),
	}

	if n := copy(cbe.docs, be.docs); n != len(be.docs) {
		panic("couldn't clone documents for base element: " + be.id)
	}

	return &cbe
}
