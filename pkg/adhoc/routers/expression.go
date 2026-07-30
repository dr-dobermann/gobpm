package routers

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/adhoc"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// expression asks a BPMN expression which activities may run next.
type expression struct {
	expr data.FormalExpression
}

// Expression returns a Router that asks a BPMN expression which activities run
// next. The expression is evaluated through the engine's language-routed
// expression seam against the container's scope, so it reads exactly the data
// the Router does and can name its successors from the case's own values.
//
// It must produce a []string of activity ids, or a single string naming one; an
// empty list or an empty string ends the container. Any other result type is a
// modeling error, reported at the decision rather than routed around.
func Expression(expr data.FormalExpression) (adhoc.Router, error) {
	if expr == nil {
		return nil, errs.New(
			errs.M("Expression: a nil expression isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return expression{expr: expr}, nil
}

func (e expression) Next(
	ctx context.Context, s adhoc.State,
) ([]string, error) {
	if s.Eval == nil {
		return nil, errs.New(
			errs.M("Expression: the routing state carries no evaluator"),
			errs.C(errorClass, errs.InvalidState))
	}

	v, err := s.Eval.Evaluate(ctx, e.expr)
	if err != nil {
		return nil, err
	}

	if v == nil {
		return nil, errs.New(
			errs.M("Expression: the routing expression produced no value"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	// A genuine branch, not a lookup: the result arities differ — a list is
	// answered as it stands, a lone id is wrapped, and an empty one stops.
	switch got := v.Get(ctx).(type) {
	case []string:
		return got, nil

	case string:
		if got == "" {
			return nil, nil
		}

		return []string{got}, nil

	default:
		return nil, errs.New(
			errs.M("Expression: the routing expression returned %T, "+
				"want []string or string", got),
			errs.C(errorClass, errs.TypeCastingError))
	}
}
