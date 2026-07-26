// Package lite implements the gobpm:lite battery expression language
// (ADR-032 §2.3, SRD-067): a small, stdlib-only text language over
// process data — float64 numbers, strings, booleans, times and nil;
// structural paths through the engine's own resolver; short-circuit
// booleans; the has/len/time builtins. The engine claims "gobpm:lite" in
// the zero-config expression registry beside the goexpr functor engine —
// out of the box a model mixes functor and text expressions freely.
package lite

import (
	"context"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

const (
	errorClass = "LITE_EXPRESSION"

	// Language is the expression-language URI the engine claims.
	Language = "gobpm:lite"

	// EngineType is the engine's ##-kind.
	EngineType = "##Lite"
)

// Engine is the gobpm:lite expression engine. Stateless: every
// evaluation parses the expression body afresh (SRD-067 §4.3 — lite
// expressions are tiny; no cache until profiling demands one).
type Engine struct{}

// interface check
var _ expression.Engine = (*Engine)(nil)

// New creates the lite Engine.
func New() *Engine {
	return &Engine{}
}

// Type returns the engine kind.
func (e *Engine) Type() string {
	return EngineType
}

// Languages returns the engine's language claims.
func (e *Engine) Languages() []string {
	return []string{Language}
}

// Evaluate interprets the expression's text body against src. The
// expression must carry a body (data.BodyHolder); when it declares a
// result type, the produced value must match it — the loud guard that
// keeps a declared-bool condition from handing a non-bool to the
// condition paths' unchecked assertion (SRD-067 §4.2).
func (e *Engine) Evaluate(
	ctx context.Context,
	expr data.FormalExpression,
	src data.Source,
) (data.Value, error) {
	if expr == nil {
		return nil, errs.New(
			errs.M("Evaluate: a nil FormalExpression isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if src == nil {
		return nil, errs.New(
			errs.M("Evaluate: a nil data Source isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	bh, ok := expr.(data.BodyHolder)
	if !ok {
		return nil, errs.New(
			errs.M("Evaluate: the expression carries no text body "+
				"(data.BodyHolder)"),
			errs.C(errorClass, errs.InvalidObject),
			errs.D("expression_id", expr.ID()))
	}

	ast, err := parse(bh.Body())
	if err != nil {
		return nil, err
	}

	res, err := (&evaluator{src: src}).eval(ctx, ast)
	if err != nil {
		return nil, err
	}

	return packResult(res, expr)
}

// packResult wraps the produced operand into a data.Value and enforces
// the declared result type. A nil result is loud — nil is a comparison
// operand, not a value an expression may produce (values.Variable cannot
// carry it).
func packResult(
	v any,
	expr data.FormalExpression,
) (data.Value, error) {
	switch x := v.(type) {
	case bool:
		return checkedResult(values.NewVariable(x), "bool", expr)

	case float64:
		return checkedResult(values.NewVariable(x), "float64", expr)

	case string:
		return checkedResult(values.NewVariable(x), "string", expr)

	case time.Time:
		return checkedResult(values.NewVariable(x), "Time", expr)

	default: // nil
		return nil, errs.New(
			errs.M("the expression produced no value (nil)"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("expression_id", expr.ID()))
	}
}

// checkedResult enforces the expression's declared result type (empty =
// undeclared, anything goes).
func checkedResult(
	val data.Value,
	produced string,
	expr data.FormalExpression,
) (data.Value, error) {
	declared := expr.ResultType()
	if declared != "" && declared != produced {
		return nil, errs.New(
			errs.M("the produced result type doesn't match the "+
				"declared one"),
			errs.C(errorClass, errs.TypeCastingError),
			errs.D("declared", declared),
			errs.D("produced", produced),
			errs.D("expression_id", expr.ID()))
	}

	return val, nil
}

// Expr mints a TextExpression pre-tagged gobpm:lite — the one-call text
// expression (ADR-032 §2.3: lite as the default text language in
// practice without any registry-level fallback).
func Expr(
	body string,
	opts ...options.Option,
) (*data.TextExpression, error) {
	return data.NewTextExpression(Language, body, opts...)
}

// Cond mints a gobpm:lite condition: Expr + the declared bool result the
// condition paths require before evaluating (SRD-066 §10). A later
// WithResultType among opts overrides deliberately.
func Cond(
	body string,
	opts ...options.Option,
) (*data.TextExpression, error) {
	return data.NewTextExpression(Language, body,
		append([]options.Option{data.WithResultType("bool")},
			opts...)...)
}
