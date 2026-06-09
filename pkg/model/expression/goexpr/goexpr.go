// Package goexpr provides the Go-native default ExpressionEngine: it delegates
// to each FormalExpression's own Evaluate method (today's behavior). Adapters
// for FEEL / JUEL / etc. replace it via thresher.WithExpressionEngine.
package goexpr

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
)

// Engine is the Go-native default expression.Engine.
type Engine struct{}

// New returns the Go-native default expression.Engine.
func New() expression.Engine { return Engine{} }

// Evaluate delegates to the expression's own Evaluate method.
func (Engine) Evaluate(
	ctx context.Context, expr data.FormalExpression, src data.Source,
) (data.Value, error) {
	return expr.Evaluate(ctx, src)
}

var _ expression.Engine = Engine{}
