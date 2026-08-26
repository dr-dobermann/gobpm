package artifacts

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// A TextAnnotation is a modeller's comment: "Text Annotations are a mechanism
// for a modeler to provide additional information for the reader of a BPMN
// Diagram" (§8.4.1). Carried for BPMN loading (ADR-039), never read by the
// engine.
type TextAnnotation struct {
	text       string
	textFormat string
	foundation.BaseElement
}

// NewTextAnnotation creates a text annotation.
//
// Both attributes are optional in the standard (0..1), so an empty text is
// accepted — an annotation is a carrier, and refusing a diagram over an empty
// comment would fail a conformant model. An empty textFormat takes the
// standard's default "text/plain" (§8.4.1).
func NewTextAnnotation(
	text, textFormat string,
	baseOpts ...options.Option,
) (*TextAnnotation, error) {
	if textFormat == "" {
		textFormat = foundation.PlainText
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, errs.New(
			errs.M("TextAnnotation creation failed"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	return &TextAnnotation{
		text:        text,
		textFormat:  textFormat,
		BaseElement: *be,
	}, nil
}

// MustTextAnnotation tries to create a text annotation and panics on error.
// For tests and examples.
func MustTextAnnotation(
	text, textFormat string,
	baseOpts ...options.Option,
) *TextAnnotation {
	ta, err := NewTextAnnotation(text, textFormat, baseOpts...)
	if err != nil {
		errs.Panic(err)
	}

	return ta
}

// Text returns the annotation's text.
func (ta *TextAnnotation) Text() string {
	return ta.text
}

// TextFormat returns the format of the annotation's text.
func (ta *TextAnnotation) TextFormat() string {
	return ta.textFormat
}

// artifact marks TextAnnotation as one of the package's carried artifact
// kinds.
func (ta *TextAnnotation) artifact() {}
