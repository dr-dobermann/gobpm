package tasks

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
)

// OutputRule extracts a value from a Complete's raw response body into a named
// output variable (ADR-021 §2.5, SRD-037 FR-7). Path is a body-path expression
// (in-process binding: gobpm expressions over Go values, reading the body as the
// "body" datum); Var is the output variable it fills; Required makes a path the
// body doesn't satisfy a fault rather than a skipped mapping.
type OutputRule struct {
	Path     data.FormalExpression
	Var      string
	Required bool
}

// ApplyOutputMapping evaluates each rule's Path over body and returns the extracted
// data to commit as the ServiceTask's output. A required path that fails to
// evaluate is an error (a worker response that violates the contract → technical
// fault); an optional one is skipped. It is the success-path twin of the
// ErrorMapper (SRD-037 §4.5): the body is exposed to Path as the "body" datum
// through the same transient Source the classifier uses.
func ApplyOutputMapping(
	ctx context.Context,
	ee expression.Engine,
	rules []OutputRule,
	body *data.ItemDefinition,
) ([]data.Data, error) {
	src := newFaultSource(Fault{Body: body})

	out := make([]data.Data, 0, len(rules))

	for _, r := range rules {
		v, err := ee.Evaluate(ctx, r.Path, src)
		if err != nil {
			if r.Required {
				return nil, errs.New(
					errs.M("output mapping: required path for %q not satisfied",
						r.Var),
					errs.C(errorClass, errs.OperationFailed),
					errs.E(err))
			}

			continue
		}

		out = append(out, data.MustParameter(r.Var,
			data.MustItemAwareElement(
				data.MustItemDefinition(v), data.ReadyDataState)))
	}

	return out, nil
}
