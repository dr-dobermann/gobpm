package data

import (
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// *****************************************************************************

// Expressions are used in many places within BPMN to extract information from
// the different elements, normally data elements. The most common usage is
// when modeling decisions, where conditional Expressions are used to direct
// the flow along specific paths based on some criteria.
//
// BPMN supports underspecified Expressions, where the logic is captured as
// natural-language descriptive text. It also supports formal Expressions,
// where the logic is captured in an executable form using a specified
// Expression language.
type Expression struct {
	foundation.BaseElement
}

// NewExpression creates a new Expression and returns its pointer on success or
// error on failure.
func NewExpression(baseOpts ...options.Option) (*Expression, error) {
	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &Expression{
		BaseElement: *be}, nil
}

// MustExperssion tires to create a new Expression and returns its pointer.
// If there is any error it panics.
func MustExpression(baseOpts ...options.Option) *Expression {
	e, err := NewExpression(baseOpts...)
	if err != nil {
		panic(err.Error())
	}

	return e
}

// *****************************************************************************

// The FormalExpression class is used to specify an executable Expression
// using a specified Expression language. A natural-language description of
// the Expression can also be specified, in addition to the formal
// specification.
// The default Expression language for all Expressions is specified in the
// Definitions element, using the expressionLanguage attribute. It can also be
// overridden on each individual FormalExpression using the same attribute.
type FormalExpression struct {
	Expression

	// Overrides the Expression language specified in the Definitions.
	// The language MUST be specified in a URI format.
	language string

	// The body of the Expression.
	// Note that this attribute is not relevant when the XML Schema is used for
	// interchange. Instead, the FormalExpression complex type supports mixed
	// content. The body of the Expression would be specified as element
	// content.
	// For example:
	// 	<formalExpression id=“ID_2">
	// 		count(../dataObject[id="CustomerRecord_1"]/emailAddress) > 0
	// 		<evaluatesToType id="ID_3" typeRef=“xsd:boolean"/>
	// 	</formalExpression>
	// body []byte

	// The type of object that this Expression returns when evaluated.
	// For example, conditional Expressions evaluate to a boolean.
	evaluatesToType *ItemDefinition
}

// NewFormalExpression creates a new FormalExpression object and
// returns its pointer or error in case of body loading error.
// func NewFormalExpression(id, lang string,
// 	body io.Reader,
// 	evalType *ItemDefinition,
// 	docs ...*foundation.Documentation,
// ) (*FormalExpression, error) {
// 	fe := FormalExpression{
// 		Expression:      *NewExpression(id, docs...),
// 		language:        lang,
// 		evaluatesToType: evalType,
// 	}

// 	if body != nil {
// 		buf := bytes.NewBuffer([]byte{})
// 		_, err := buf.ReadFrom(body)
// 		if err != nil {
// 			return nil, &errs.ApplicationError{
// 				Err:     err,
// 				Message: "couldn't read body",
// 				Classes: []string{errs.InvalidObject},
// 			}
// 		}

// 		if buf.Len() > 0 {
// 			fe.body = make([]byte, buf.Len())
// 			copy(fe.body, buf.Bytes())
// 		}
// 	}

// 	return &fe, nil
// }

// Language returns FormalExpression language settings.
func (fe *FormalExpression) Language() string {
	return fe.language
}

// EvalType returns the FormalExpression evaluation type if set.
func (fe *FormalExpression) EvalType() *ItemDefinition {
	return fe.evaluatesToType
}
