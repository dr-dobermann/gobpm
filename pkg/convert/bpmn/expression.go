package bpmn

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// formalExpression is a text-carrying data.FormalExpression built from a
// <bpmn:conditionExpression> body. It preserves the expression text and its
// language so a condition survives an import → export round-trip
// (SRD-051 §FR-5/§FR-8, open question 2).
//
// The converter is not an expression engine: Evaluate always fails. To make
// an imported conditional flow executable, replace the condition with a
// compiled expression (e.g. data/goexpr) of the target engine.
type formalExpression struct {
	id       string
	language string
	body     string
}

// newFormalExpression creates a text-carrying condition with the given id,
// language (URI, may be empty) and expression body.
func newFormalExpression(id, language, body string) *formalExpression {
	return &formalExpression{id: id, language: language, body: body}
}

// Body returns the raw expression text — the accessor Export uses to write
// the condition back (data.FormalExpression itself has no text getter).
func (e *formalExpression) Body() string { return e.body }

// ID implements foundation.Identifyer.
func (e *formalExpression) ID() string { return e.id }

// Docs implements foundation.Documentator.
func (*formalExpression) Docs() []*foundation.Documentation { return nil }

// Language implements data.FormalExpression.
func (e *formalExpression) Language() string { return e.language }

// Evaluate implements data.FormalExpression. It always fails: the converter
// carries the expression text but does not interpret it.
func (e *formalExpression) Evaluate(_ context.Context, _ data.Source) (data.Value, error) {
	return nil, errs.New(
		errs.M("bpmn condition %q: converter does not evaluate expressions (language %q)",
			e.id, e.language),
		errs.C(errorClass, errs.ConditionFailed))
}

// Result implements data.FormalExpression.
func (e *formalExpression) Result() (data.Value, error) {
	return nil, errs.New(
		errs.M("bpmn condition %q: expression was never evaluated", e.id),
		errs.C(errorClass, errs.ConditionFailed))
}

// ResultType implements data.FormalExpression. Sequence-flow conditions are
// boolean by definition (BPMN §13.2), and gateways require "bool".
func (*formalExpression) ResultType() string { return typeBool }

// IsEvaluated implements data.FormalExpression.
func (*formalExpression) IsEvaluated() bool { return false }

var _ data.FormalExpression = (*formalExpression)(nil)
