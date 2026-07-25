// Package goexpr provides the Go-native default ExpressionEngine: it delegates
// to each FormalExpression's own Evaluate method (today's behavior). Adapters
// for FEEL / JUEL / etc. replace it via thresher.WithExpressionEngine.
package goexpr

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	dgexpr "github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
)

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
func (Engine) Evaluate(
	ctx context.Context, expr data.FormalExpression, src data.Source,
) (data.Value, error) {
	return expr.Evaluate(ctx, src)
}

var _ expression.Engine = Engine{}
