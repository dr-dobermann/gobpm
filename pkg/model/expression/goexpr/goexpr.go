// Package goexpr provides the Go-native default ExpressionEngine: it delegates
// to each FormalExpression's own Evaluate method (today's behavior). Adapters
// for FEEL / JUEL / etc. replace it via thresher.WithExpressionEngine.
package goexpr

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	dgexpr "github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
)

const errorClass = "GOEXPR_ENGINE"

// GoExprType is the engine kind (the "##"-hint convention).
const GoExprType = "##GoExpr"

// Engine is the Go-native default expression.Engine: it claims the
// functor kind's language and delegates to each expression's own Evaluate.
type Engine struct{}

// New returns the Go-native default expression.Engine.
func New() expression.Engine { return Engine{} }

// Type returns the engine kind.
func (Engine) Type() string { return GoExprType }

// Languages returns the functor kind's language claim.
func (Engine) Languages() []string {
	return []string{dgexpr.Language}
}

// Evaluate delegates to the expression's own Evaluate method.
//
// The nil-expression guard is not ceremony around a one-line delegation: this
// Engine is a public extension point, so a nil expression reaching it is a
// caller's bug that must be named here. Without it the delegation dereferences
// nil and panics inside the engine, reporting the library as broken rather
// than the call.
//
// A nil SOURCE is deliberately NOT rejected, unlike in lite.Engine. A
// GExpression may carry its own source, bound at construction, and
// substituteSource uses it precisely when the passed one is nil — so a
// self-sourced functor evaluating with no caller-supplied source is correct
// here and meaningless there. The difference is real, not an oversight.
func (Engine) Evaluate(
	ctx context.Context, expr data.FormalExpression, src data.Source,
) (data.Value, error) {
	if expr == nil {
		return nil, errs.New(
			errs.M("Evaluate: a nil FormalExpression isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return expr.Evaluate(ctx, src)
}

var _ expression.Engine = Engine{}
