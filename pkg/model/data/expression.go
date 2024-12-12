package data

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// The Expression class is used to specify an Expression using natural-language
// text. These Expressions are not executable. The natural language text is
// captured using the documentation attribute, inherited from BaseElement.
// Expression inherits the attributes and model associations of BaseElement
// but adds no additional attributes or model associations.
//
// Business Process Model and Notation (BPMN), v2.0.2 83 Expressions are used
// in many places within BPMN to extract information from the different
// elements, normally data elements. The most common usage is when modeling
// decisions, where conditional Expressions are used to direct the flow along
// specific paths based on some criteria.
//
// BPMN supports underspecified Expressions, where the logic is captured as
// natural-language descriptive text. It also supports formal Expressions,
// where the logic is captured in an executable form using a specified
// Expression language.

// Expression
//
// The Expression class is used to specify an Expression using natural-language
// text. These Expressions are not executable and are considered underspecified.
// The definition of an Expression can be done in two ways: it can be contained
// where it is used, or it can be defined at the Process level and then
// referenced where it is used.
// The Expression element inherits the attributes and model associations of
// BaseElement (see Table 8.5), but does not have any additional attributes or
// model associations.
type Expression struct {
	foundation.BaseElement
}

// NewExpression creates an Expression with optional Id and Docs from foundation.
func NewExpression(opts ...options.Option) (*Expression, error) {
	be, err := foundation.NewBaseElement(opts...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("expression building failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return &Expression{
			BaseElement: *be,
		},
		nil
}

// ============================================================================

// Formal Expression
//
// The FormalExpression class is used to specify an executable Expression using
// a specified Expression language. A natural-language description of the
// Expression can also be specified, in addition to the formal
// specification.
// The default Expression language for all Expressions is specified in the
// Definitions element, using the expressionLanguage attribute. It can also be
// overridden on each individual FormalExpression using the same attribute.
// The FormalExpression element inherits the attributes and model associations
// of BaseElement, through the Expression element.
type FormalExpression interface {
	foundation.Identifyer

	foundation.Documentator

	// Language returns the FormalExpression language in URI format.
	Language() string

	// Evaluate evaluate the expression and returns its result.
	// Evaluate doesn't check if Source is nil. If the expresssion demands
	// external data it should check source nillability by itself.
	Evaluate(ctx context.Context, source Source) (Value, error)

	// Result returns evaluated result of the formal expression.
	// If there is no evaluation was made, an error returned.
	Result() (Value, error)

	// ResultType returns name of the FormalExpression result type.
	ResultType() string

	// IsEvaluated returns true if result is ready.
	IsEvaluated() bool
}
