// Package expression defines the ExpressionEngine extension: the engine-level
// indirection through which BPMN FormalExpressions are evaluated, so the
// evaluation strategy (Go-native, FEEL, JUEL, …) is swappable. FormalExpression
// itself stays a BPMN model element in pkg/model/data; ExpressionEngine wraps its
// evaluation. The Go-native default lives in the goexpr sibling subpackage
// (ADR-002 §4.2; ADR-003). It lives under pkg/model/ because it evaluates a BPMN
// spec concept.
package expression

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// Engine evaluates a BPMN FormalExpression against a data source (the
// "ExpressionEngine" extension of ADR-002 §4.2; named Engine here to avoid the
// expression.ExpressionEngine stutter). It mirrors the FormalExpression
// evaluation signature so the default is a thin pass-through and adapters can
// intercept at one point.
type Engine interface {
	// Type names the engine kind in the "##"-hint convention ("##GoExpr",
	// "##Lite", "##FEEL", ...) for the startup config.
	Type() string

	// Languages returns the expression-language URIs this engine
	// evaluates — an enumerable claim (never empty for a real engine,
	// matched case-insensitively), so the Registry can detect conflicts
	// and print the routing table (ADR-032 §2.1).
	Languages() []string

	// Evaluate evaluates expr against src and returns the result.
	Evaluate(ctx context.Context, expr data.FormalExpression, src data.Source) (data.Value, error)
}
