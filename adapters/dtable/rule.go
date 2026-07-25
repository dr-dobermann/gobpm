package dtable

import (
	"context"
	"maps"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/rules"
)

// Rule is the ADR-027 behavior contract a decision table row implements:
// match against the input context, yield outputs. The functor-backed kind
// below is the first implementation; a compiled-definition row (a future
// deployable format's) implements the same contract without touching the
// table machinery.
type Rule interface {
	// Matches reports whether the rule's conditions hold over the read
	// surface.
	Matches(ctx context.Context, r service.DataReader) (bool, error)

	// Yield produces the rule's result row. It runs only for matching
	// rules.
	Yield(ctx context.Context, r service.DataReader) (rules.Row, error)
}

// YieldFunc computes a rule's outputs from the read surface — the
// computed-output twin of a static Then row.
type YieldFunc func(
	ctx context.Context,
	r service.DataReader,
) (rules.Row, error)

// fRule is the functor-backed rule kind: an AND over condition cells plus a
// yield.
type fRule struct {
	yield YieldFunc
	conds []Condition
}

// Matches evaluates the condition cells in order, short-circuiting on the
// first false or failure. An empty condition set matches always (the
// all-"-" row).
func (fr *fRule) Matches(
	ctx context.Context, r service.DataReader,
) (bool, error) {
	for _, c := range fr.conds {
		ok, err := c(ctx, r)
		if err != nil || !ok {
			return false, err
		}
	}

	return true, nil
}

// Yield produces the rule's result row.
func (fr *fRule) Yield(
	ctx context.Context, r service.DataReader,
) (rules.Row, error) {
	return fr.yield(ctx, r)
}

// interface check
var _ Rule = (*fRule)(nil)

// RuleBuilder assembles a functor rule from condition cells; finish with
// Then (static outputs) or ThenF (computed outputs).
type RuleBuilder struct {
	err   error
	conds []Condition
}

// R starts a rule row from condition cells. An empty cell list builds a
// match-always row; a nil cell is rejected at Then/ThenF.
func R(conds ...Condition) *RuleBuilder {
	b := &RuleBuilder{conds: conds}

	for i, c := range conds {
		if c == nil {
			b.err = errs.New(
				errs.M("R: a nil Condition isn't allowed (cell %d)", i),
				errs.C(errorClass, errs.EmptyNotAllowed))

			break
		}
	}

	return b
}

// Then finishes the rule with static outputs (a copy of out).
func (b *RuleBuilder) Then(out rules.Row) (Rule, error) {
	if b.err != nil {
		return nil, b.err
	}

	if len(out) == 0 {
		return nil, errs.New(
			errs.M("Then: an empty output row isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	fixed := maps.Clone(out)

	return &fRule{
		conds: b.conds,
		yield: func(context.Context, service.DataReader) (rules.Row, error) {
			return fixed, nil
		},
	}, nil
}

// ThenF finishes the rule with a computed-output functor.
func (b *RuleBuilder) ThenF(f YieldFunc) (Rule, error) {
	if b.err != nil {
		return nil, b.err
	}

	if f == nil {
		return nil, errs.New(
			errs.M("ThenF: a nil YieldFunc isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return &fRule{conds: b.conds, yield: f}, nil
}
