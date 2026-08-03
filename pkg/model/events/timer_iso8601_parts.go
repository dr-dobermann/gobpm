package events

import (
	"context"
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/iso8601"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// formOf reports which ISO 8601 grammar a literal belongs to. The three are
// disjoint by their leading designator, so the text decides: R… repeats, P… is
// a duration, anything else is read as a date-time.
func formOf(s string) TimerForm {
	switch {
	case strings.HasPrefix(s, "R"):
		return Cycle

	case strings.HasPrefix(s, "P"):
		return Duration
	}

	return Time
}

// isoErr reports an unparseable literal by naming ALL THREE accepted shapes.
// The underlying parser can only complain about the grammar it guessed, so a
// string like "tomorrow" would otherwise be refused for "not a date-time" —
// true, but useless to someone who meant to write a duration.
func isoErr(s string, cause error) error {
	return errs.New(
		errs.M("NewISO8601Timer: %q is not an ISO 8601 timer — expected a "+
			"date-time (2011-03-11T12:13:14Z), a duration (P10D, PT10H) or "+
			"a bounded recurrence (R3/PT10H)", s),
		errs.C(errorClass, errs.InvalidParameter),
		errs.E(cause))
}

// constExpr wraps an already-parsed value as a FormalExpression, so a literal
// timer produces exactly the definition a positional constructor would.
func constExpr[T any](id string, v T) data.FormalExpression {
	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(v)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(v), nil
		},
		foundation.WithID(id))
}

// isoAdapter turns an expression yielding an ISO 8601 STRING into one yielding
// the Go value a timer attribute expects, by parsing at evaluation time.
//
// This is what keeps a dynamic timer off the runtime: the waiter still
// evaluates a FormalExpression and reads a typed value out of it, exactly as it
// does for a static one. The parsing lives here, not there.
func isoAdapter[T any](
	e data.FormalExpression,
	form TimerForm,
	convert func(string) (T, error),
) data.FormalExpression {
	var zero T

	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(zero)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			v, err := e.Evaluate(ctx, ds)
			if err != nil {
				return nil, err
			}

			s, ok := v.Get(ctx).(string)
			if !ok {
				return nil, errs.New(
					errs.M("timer %s expression must evaluate to an ISO 8601 "+
						"string", string(form)),
					errs.C(errorClass, errs.TypeCastingError))
			}

			out, err := convert(s)
			if err != nil {
				return nil, errs.New(
					errs.M("timer %s expression yielded %q", string(form), s),
					errs.C(errorClass, errs.InvalidObject),
					errs.E(err))
			}

			return values.NewVariable(out), nil
		})
}

// The four conversions the adapters install, one per attribute a form fills.
func dateOf(s string) (time.Time, error) { return iso8601.ParseDateTime(s) }

func durationOf(s string) (time.Duration, error) {
	return iso8601.ParseDuration(s)
}

func countOf(s string) (int, error) {
	r, err := iso8601.ParseRepeat(s)

	return r.Count, err
}

func intervalOf(s string) (time.Duration, error) {
	r, err := iso8601.ParseRepeat(s)

	return r.Interval, err
}
