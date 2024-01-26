package foundation

import "github.com/google/uuid"

const (
	defaultDocFormat = "text/plain"
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
func NewBaseElement(id string, docs ...*Documentation) *BaseElement {
	if id == "" {
		id = uuid.Must(uuid.NewRandom()).String()
	}

	be := BaseElement{
		id:   id,
		docs: make([]Documentation, len(docs)),
	}

	for i, d := range docs {
		be.docs[i] = *d
	}

	return &be
}

// Id returns the BaseElement Id.
func (be BaseElement) Id() string {
	return be.id
}

// Docs returns the copy of BaseElement documentation.
func (be BaseElement) Docs() []Documentation {
	return append([]Documentation{}, be.docs...)
}
