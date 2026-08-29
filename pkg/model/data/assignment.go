package data

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// ============================================================================
//                          Assignment
// ============================================================================

// Assignment is one from→to mapping of a data association (BPMN §10.4.2
// rule 2, ADR-011 §2.4): from is evaluated for a value, to is the data path
// that value is written to.
//
// The two halves are deliberately not the same kind of thing. from is an
// ordinary expression, evaluated through the engine's ExpressionEngine
// against the activity's data context. to is a PATH, not an expression: the
// standard defines it as an Expression yielding "any element in context or
// sub-element of it", which in the general case needs an evaluator that
// returns a location rather than a value; a path addresses everything this
// engine holds, and the narrowing is ADR-011 §2.4's engine choice.
//
// The path is absolute: its head names the association's TARGET — the scope
// datum on an output association, the activity's parameter on an input one —
// and the remaining steps address inside it. A head-only path ("order") is
// the whole-value write; "order.status" writes one field of it.
type Assignment struct {
	from FormalExpression
	to   string
	foundation.BaseElement
}

// NewAssignment creates one from→to mapping.
//
// from must evaluate to the value being written; to must be a parseable data
// path whose head names the association's target. Neither may be empty: an
// assignment with no source expression writes nothing, and one with no target
// path has nowhere to write.
func NewAssignment(
	from FormalExpression,
	to string,
	baseOpts ...options.Option,
) (*Assignment, error) {
	if from == nil {
		return nil, errs.New(
			errs.M("NewAssignment: a nil from expression isn't allowed — "+
				"an assignment evaluates it for the value it writes"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	to = strings.TrimSpace(to)
	if to == "" {
		return nil, errs.New(
			errs.M("NewAssignment: a blank to path isn't allowed — "+
				"an assignment writes at a path (ADR-011 §2.4)"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if _, _, err := SplitPath(to); err != nil {
		return nil, errs.New(
			errs.M("NewAssignment: to %q isn't a data path", to),
			errs.C(errorClass, errs.InvalidParameter),
			errs.E(err))
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, errs.New(
			errs.M("NewAssignment: couldn't build the base element"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	return &Assignment{
		from:        from,
		to:          to,
		BaseElement: *be,
	}, nil
}

// From returns the expression evaluated for the value to write.
func (a *Assignment) From() FormalExpression {
	return a.from
}

// To returns the absolute data path the value is written at.
func (a *Assignment) To() string {
	return a.to
}

// ToHead splits the to path into the head — which names the association's
// target — and the RELATIVE remainder addressing inside it. An empty
// remainder means the whole value is replaced. It cannot fail: the path
// was validated at construction. Split here rather than at
// every consumer so the validator, the copy path and the converter all read
// one answer, and the remainder is returned as a path string because that is
// what a structural write takes.
func (a *Assignment) ToHead() (head, rest string) {
	// NewAssignment refused a to that SplitPath rejects, and to is
	// immutable, so the split cannot fail here — returning an error would
	// only give every caller a branch it can never take. The error is
	// discarded explicitly rather than ignored, so a reader sees the
	// invariant rather than an oversight.
	head, _, err := SplitPath(a.to)
	if err != nil {
		return a.to, ""
	}

	return head, strings.TrimPrefix(a.to[len(head):], ".")
}
