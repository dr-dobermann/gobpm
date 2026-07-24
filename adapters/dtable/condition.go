// Package dtable is the Decision Table rule engine (ADR-029): a pluggable
// rules.Engine adapter evaluating DMN-shaped decision tables — a hit policy
// over an ordered rule list — with Go functors as the rule expressions. The
// table is data; a rule is behavior (match + yield); conditions read the
// same service.DataReader walk-up every in-process gobpm functor receives.
//
// Missing input fails loud by default (a classified error naming the
// decision, rule and datum — the fail-loud house rule, a deliberate
// deviation from DMN's null-tolerant fall-through); the per-condition
// IfPresent combinator opts into the DMN no-match semantics.
package dtable

import (
	"context"
	"errors"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

const (
	errorClass = "DTABLE"

	// DTableType is the engine's implementation kind (the "##"-hint
	// convention of ADR-027 §2.2).
	DTableType = "##DTable"
)

// Condition is one cell of a rule row: a predicate over the read-only
// process-data surface. Constructors below cover the common comparisons;
// Pred is the raw escape hatch.
type Condition func(ctx context.Context, r service.DataReader) (bool, error)

// ErrAbsent marks a condition failure caused by a datum read (the datum is
// missing or unreadable) — the only failure class IfPresent converts into a
// no-match. It is exposed so a Pred author can classify a custom read the
// same way (errors.Join(dtable.ErrAbsent, cause)).
var ErrAbsent = errors.New("dtable: condition datum absent")

// value reads the named datum's current value, failing loud when absent
// (wrap with IfPresent for the DMN no-match semantics). The returned error
// carries ErrAbsent in its chain.
func value(
	ctx context.Context, r service.DataReader, datum string,
) (any, error) {
	d, err := r.GetData(datum)
	if err != nil {
		return nil, errors.Join(
			ErrAbsent,
			errs.New(
				errs.M("couldn't read condition datum"),
				errs.C(errorClass, errs.ObjectNotFound),
				errs.E(err),
				errs.D("datum", datum)))
	}

	return d.Value().Get(ctx), nil
}

// compare orders two values of the same supported type (int, int64,
// float64, string): -1, 0 or 1. Differing or unsupported types are a
// classified error — never a silent false (ADR-029 §2.2; deployed JSON
// literals land as float64 and are deliberately not coerced).
func compare(got, want any) (int, error) {
	switch g := got.(type) {
	case int:
		if w, ok := want.(int); ok {
			return order(g, w), nil
		}

	case int64:
		if w, ok := want.(int64); ok {
			return order(g, w), nil
		}

	case float64:
		if w, ok := want.(float64); ok {
			return order(g, w), nil
		}

	case string:
		if w, ok := want.(string); ok {
			return order(g, w), nil
		}
	}

	return 0, errs.New(
		errs.M("condition operands aren't comparable"),
		errs.C(errorClass, errs.TypeCastingError),
		errs.D("datum_type", reflect.TypeOf(got).String()),
		errs.D("operand_type", reflect.TypeOf(want).String()))
}

// order compares two ordered values of the same type.
func order[T int | int64 | float64 | string](a, b T) int {
	switch {
	case a < b:
		return -1

	case a > b:
		return 1
	}

	return 0
}

// ordered builds a comparison condition passing when compare(datum, than)
// lands in the accepted set.
func ordered(datum string, than any, accept ...int) Condition {
	return func(ctx context.Context, r service.DataReader) (bool, error) {
		got, err := value(ctx, r, datum)
		if err != nil {
			return false, err
		}

		c, err := compare(got, than)
		if err != nil {
			return false, condErr(datum, err)
		}

		for _, a := range accept {
			if c == a {
				return true, nil
			}
		}

		return false, nil
	}
}

// condErr classifies a condition evaluation failure on datum.
func condErr(datum string, err error) error {
	return errs.New(
		errs.M("condition evaluation failed"),
		errs.C(errorClass, errs.OperationFailed),
		errs.E(err),
		errs.D("datum", datum))
}

// Eq passes when the datum's value deep-equals want.
func Eq(datum string, want any) Condition {
	return func(ctx context.Context, r service.DataReader) (bool, error) {
		got, err := value(ctx, r, datum)
		if err != nil {
			return false, err
		}

		return reflect.DeepEqual(got, want), nil
	}
}

// NE passes when the datum's value does not deep-equal want.
func NE(datum string, want any) Condition {
	eq := Eq(datum, want)

	return func(ctx context.Context, r service.DataReader) (bool, error) {
		ok, err := eq(ctx, r)

		return !ok && err == nil, err
	}
}

// GT passes when the datum's value is strictly greater than than.
func GT(datum string, than any) Condition {
	return ordered(datum, than, 1)
}

// GE passes when the datum's value is greater than or equal to than.
func GE(datum string, than any) Condition {
	return ordered(datum, than, 1, 0)
}

// LT passes when the datum's value is strictly less than than.
func LT(datum string, than any) Condition {
	return ordered(datum, than, -1)
}

// LE passes when the datum's value is less than or equal to than.
func LE(datum string, than any) Condition {
	return ordered(datum, than, -1, 0)
}

// Between passes when lo <= datum's value <= hi (inclusive bounds).
func Between(datum string, lo, hi any) Condition {
	ge, le := GE(datum, lo), LE(datum, hi)

	return func(ctx context.Context, r service.DataReader) (bool, error) {
		ok, err := ge(ctx, r)
		if err != nil || !ok {
			return false, err
		}

		return le(ctx, r)
	}
}

// In passes when the datum's value deep-equals any member of set.
func In(datum string, set ...any) Condition {
	return func(ctx context.Context, r service.DataReader) (bool, error) {
		got, err := value(ctx, r, datum)
		if err != nil {
			return false, err
		}

		for _, want := range set {
			if reflect.DeepEqual(got, want) {
				return true, nil
			}
		}

		return false, nil
	}
}

// Any always passes — DMN's "-" (irrelevant) test. It never reads data, so
// it matches even when every datum is absent.
func Any() Condition {
	return func(context.Context, service.DataReader) (bool, error) {
		return true, nil
	}
}

// Pred wraps a raw predicate as a Condition. A nil predicate is rejected at
// evaluation with a classified error (the builder cannot return one here
// without breaking the declarative grid shape).
func Pred(fn Condition) Condition {
	if fn == nil {
		return func(context.Context, service.DataReader) (bool, error) {
			return false, errs.New(
				errs.M("Pred: a nil predicate isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}
	}

	return fn
}

// IfPresent converts a failed datum READ inside c (an ErrAbsent-marked
// failure) into a plain no-match — the DMN null-tolerant semantics, opted
// into per condition (ADR-029 §2.5). Every other failure — a type
// mismatch, a failing Pred — stays loud: tolerance covers absence, not
// broken conditions.
func IfPresent(c Condition) Condition {
	if c == nil {
		return Pred(nil)
	}

	return func(ctx context.Context, r service.DataReader) (bool, error) {
		ok, err := c(ctx, r)
		if err != nil && errors.Is(err, ErrAbsent) {
			return false, nil
		}

		return ok, err
	}
}
