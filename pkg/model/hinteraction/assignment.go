package hinteraction

import (
	"context"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
)

// AssignmentSlot identifies which Camunda-style human-task role an Assignment
// fills. The slot determines which Actor field the resolved set is matched
// against: Assignee and CandidateUsers match the Actor's UserID; CandidateGroups
// matches the Actor's Groups (ADR-020 §2.5).
type AssignmentSlot int

const (
	// Assignee is the task's actual owner: when set, only a matching UserID may
	// read or complete the task (the restrictive gate).
	Assignee AssignmentSlot = iota

	// CandidateUsers are the user identities eligible to claim/complete the task.
	CandidateUsers

	// CandidateGroups are the group identities whose members may claim/complete.
	CandidateGroups
)

// String returns the slot's Camunda name.
func (s AssignmentSlot) String() string {
	switch s {
	case Assignee:
		return "assignee"

	case CandidateUsers:
		return "candidateUsers"

	case CandidateGroups:
		return "candidateGroups"
	}

	return "unknown"
}

// Assignment is one member of a UserTask's authorization triad: the identifiers
// for a single slot, given either as static literals OR as a FormalExpression
// that evaluates to a list of identifiers at authorization time (the BPMN
// resourceAssignmentExpression, which "MUST return Resource entity related data
// types, like Users or Groups"). The two forms are mutually exclusive. The
// standard ResourceRole cannot carry this (no user/group distinction, no static
// id-list, no slot), so the triad is its own typed structure (ADR-020 §2.5).
type Assignment struct {
	expr   data.FormalExpression
	static []string
	slot   AssignmentSlot
}

// NewStaticAssignment builds an Assignment for slot from one or more non-empty
// static identifiers. It errors on an empty identifier or an empty list.
func NewStaticAssignment(
	slot AssignmentSlot,
	ids ...string,
) (*Assignment, error) {
	clean := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, errs.New(
				errs.M("NewStaticAssignment: an empty identifier isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed),
				errs.D("slot", slot.String()))
		}

		clean = append(clean, id)
	}

	if len(clean) == 0 {
		return nil, errs.New(
			errs.M("NewStaticAssignment: at least one identifier is required"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D("slot", slot.String()))
	}

	return &Assignment{slot: slot, static: clean}, nil
}

// NewExprAssignment builds an Assignment for slot from a FormalExpression that
// evaluates to a list of identifiers. It errors on a nil expression.
func NewExprAssignment(
	slot AssignmentSlot,
	expr data.FormalExpression,
) (*Assignment, error) {
	if expr == nil {
		return nil, errs.New(
			errs.M("NewExprAssignment: a nil expression isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D("slot", slot.String()))
	}

	return &Assignment{slot: slot, expr: expr}, nil
}

// Slot returns the assignment's slot.
func (a *Assignment) Slot() AssignmentSlot {
	return a.slot
}

// Resolve returns the identifier set for the Assignment: the static list, or the
// FormalExpression evaluated over src (through eng) coerced to a list of strings.
// A nil result, a nil engine, or an evaluation failure yields an empty set —
// BPMN treats a failed Resource query as one returning an empty result set, so a
// broken assignment authorizes no one rather than everyone.
func (a *Assignment) Resolve(
	ctx context.Context,
	src data.Source,
	eng expression.Engine,
) []string {
	if a.expr == nil {
		return append([]string{}, a.static...)
	}

	if eng == nil {
		return nil
	}

	val, err := eng.Evaluate(ctx, a.expr, src)
	if err != nil || val == nil {
		return nil
	}

	return toIdentifiers(val.Get(ctx))
}

// toIdentifiers coerces an expression result into a list of identifiers. It
// accepts a []string, a []any of strings, or a single non-empty string; any
// other shape (including nil) yields an empty list.
func toIdentifiers(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return append([]string{}, v...)

	case string:
		if v == "" {
			return nil
		}

		return []string{v}

	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}

		return out
	}

	return nil
}
