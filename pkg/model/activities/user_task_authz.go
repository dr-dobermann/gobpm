package activities

import (
	"context"
	"slices"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
)

// Authorizer decides whether an Actor may act on a task by resolving the task's
// assignment triad against the runtime data and checking membership. It is
// implemented by UserTask and called at BOTH Take and Complete (ADR-020 §2.4).
type Authorizer interface {
	Authorize(
		ctx context.Context,
		actor hi.Actor,
		src data.Source,
		eng expression.Engine,
	) error
}

// OutputValidator validates submitted outputs against the task's output
// specification. Implemented by UserTask and called at Complete only.
type OutputValidator interface {
	ValidateOutputs(outputs []data.Data) error
}

// Assignments returns the UserTask's declared triad members (assignee, candidate
// users, candidate groups) in slot order, skipping undeclared slots. It is the
// typed accessor for the triad — the single source of truth, coexisting with the
// generic activity Roles() rather than projected into it (ADR-020 §2.5).
func (ut *UserTask) Assignments() []*hi.Assignment {
	res := make([]*hi.Assignment, 0, 3)
	for _, a := range []*hi.Assignment{
		ut.assignee, ut.candidateUsers, ut.candidateGroups,
	} {
		if a != nil {
			res = append(res, a)
		}
	}

	return res
}

// Authorize reports whether actor may act on the task, per ADR-020 §2.5: if an
// assignee is declared, only a matching UserID is authorized (the restrictive
// gate); otherwise a matching candidateUser OR an intersecting candidateGroup
// authorizes; a task with no triad member declared is open to any actor. A
// failed/empty expression resolves to an empty set, i.e. denies. A nil verdict
// means authorized; a non-nil error is a non-terminal denial (the caller keeps
// the task parked and waits for the right actor).
func (ut *UserTask) Authorize(
	ctx context.Context,
	actor hi.Actor,
	src data.Source,
	eng expression.Engine,
) error {
	if actor == nil {
		return errs.New(
			errs.M("Authorize: a nil Actor isn't allowed"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("task_id", ut.ID()))
	}

	// No triad declared → open (BPMN's unspecified performer).
	if ut.assignee == nil &&
		ut.candidateUsers == nil &&
		ut.candidateGroups == nil {
		return nil
	}

	if ut.assignee != nil {
		if slices.Contains(
			ut.assignee.Resolve(ctx, src, eng), actor.UserID()) {
			return nil
		}

		return ut.unauthorized(actor)
	}

	if ut.candidateUsers != nil &&
		slices.Contains(
			ut.candidateUsers.Resolve(ctx, src, eng), actor.UserID()) {
		return nil
	}

	if ut.candidateGroups != nil &&
		intersects(
			ut.candidateGroups.Resolve(ctx, src, eng), actor.Groups()) {
		return nil
	}

	return ut.unauthorized(actor)
}

// unauthorized builds the non-terminal authorization-failure error (the task
// stays parked; the process is unaffected). It self-identifies the task and the
// rejected user, never the task's data.
func (ut *UserTask) unauthorized(actor hi.Actor) error {
	return errs.New(
		errs.M("actor %q is not authorized for user task %q",
			actor.UserID(), ut.ID()),
		errs.C(errorClass, errs.ConditionFailed),
		errs.D("task_id", ut.ID()),
		errs.D("user_id", actor.UserID()))
}

// ValidateOutputs checks submitted outputs against the task's output spec
// (Outputs()): every required parameter must be present by name, every provided
// output must correspond to a declared parameter (no unknown extras), and a
// present output's value type must match its declared parameter type. Failure is
// non-terminal — the caller keeps the task parked and the actor resubmits.
func (ut *UserTask) ValidateOutputs(outputs []data.Data) error {
	params := ut.Outputs()

	provided := make(map[string]data.Data, len(outputs))
	for _, d := range outputs {
		if d == nil {
			return errs.New(
				errs.M("ValidateOutputs: a nil output isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed),
				errs.D("task_id", ut.ID()))
		}

		provided[d.Name()] = d
	}

	known := make(map[string]struct{}, len(params))
	for _, p := range params {
		known[p.Name()] = struct{}{}

		d, ok := provided[p.Name()]
		if !ok {
			if p.IsRequired() {
				return errs.New(
					errs.M("ValidateOutputs: required output %q is missing",
						p.Name()),
					errs.C(errorClass, errs.EmptyNotAllowed),
					errs.D("task_id", ut.ID()),
					errs.D("output", p.Name()))
			}

			continue
		}

		if err := checkOutputType(p.Name(), p.Type(), d, ut.ID()); err != nil {
			return err
		}
	}

	for name := range provided {
		if _, ok := known[name]; !ok {
			return errs.New(
				errs.M("ValidateOutputs: unknown output %q", name),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("task_id", ut.ID()),
				errs.D("output", name))
		}
	}

	return nil
}

// checkOutputType rejects a provided output whose value type differs from the
// declared parameter type (a ResourceParameter always carries a non-empty type).
// A value-less datum reports type "", which mismatches any typed parameter.
func checkOutputType(name, want string, d data.Data, taskID string) error {
	got := ""
	if v := d.Value(); v != nil {
		got = v.Type()
	}

	if got != want {
		return errs.New(
			errs.M("ValidateOutputs: output %q type mismatch: want %q, got %q",
				name, want, got),
			errs.C(errorClass, errs.TypeCastingError),
			errs.D("task_id", taskID),
			errs.D("output", name))
	}

	return nil
}

// intersects reports whether any element of a is in b.
func intersects(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}

	return false
}
