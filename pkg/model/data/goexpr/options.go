package goexpr

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// GExpOption configures a GExpression at construction. New dispatches it
// by type-switch; every other options.Option forwards to the embedded
// data.Expression unchanged.
type GExpOption func(ge *GExpression) error

// Option marks GExpOption as an options.Option.
func (GExpOption) Option() {}

// WithDependencies declares the data paths the expression's evaluation
// function reads (data.DependencyLister, ADR-006 v.3 §2.7): a conditional
// subscription over the expression then re-evaluates only on commits whose
// changed paths overlap a declared one, instead of on every non-empty
// commit. The declaration is the author's contract — a wrong list means a
// missed wake-up, so declare exactly what the function reads.
//
// At least one path is required — an empty declaration would mean "never
// re-evaluate", the degenerate trap — and every path must be non-empty and
// parse under the structural grammar.
func WithDependencies(paths ...string) GExpOption {
	return GExpOption(func(ge *GExpression) error {
		if len(paths) == 0 {
			return errs.New(
				errs.M("WithDependencies: at least one path is required"),
				errs.C(errorClass, errs.InvalidParameter))
		}

		for _, p := range paths {
			if p == "" {
				return errs.New(
					errs.M("WithDependencies: an empty path isn't allowed"),
					errs.C(errorClass, errs.InvalidParameter))
			}

			if _, _, err := data.SplitPath(p); err != nil {
				return errs.New(
					errs.M("WithDependencies: invalid path %q", p),
					errs.C(errorClass, errs.InvalidParameter),
					errs.E(err))
			}
		}

		ge.deps = append(ge.deps, paths...)

		return nil
	})
}
