package activities

import (
	"context"
	"github.com/dr-dobermann/gobpm/pkg/observability"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
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

// ResolveEligibility resolves the task's assignment triad against src via eng into
// a frozen interactor.Eligibility (ADR-020 v.2 §2.7). It is called once, when the
// task is distributed and its instance is still resident; every later
// authorization check reads that snapshot instead of re-resolving, so a candidate
// set cannot shift under a waiting task and an owner cannot lose the ability to
// finish work it already holds.
//
// Each slot records whether the model declared it, independently of what it
// resolved to: a declared slot resolving to an empty set authorizes no one (BPMN
// treats a failed resource query as an empty result set), while an undeclared slot
// is absent from the verdict.
func (ut *UserTask) ResolveEligibility(
	ctx context.Context,
	src data.Source,
	eng expression.Engine,
) interactor.Eligibility {
	return interactor.Eligibility{
		Assignee:        resolveSlot(ctx, ut.assignee, src, eng),
		CandidateUsers:  resolveSlot(ctx, ut.candidateUsers, src, eng),
		CandidateGroups: resolveSlot(ctx, ut.candidateGroups, src, eng),
	}
}

// resolveSlot resolves one optional triad member; a nil member is undeclared.
func resolveSlot(
	ctx context.Context,
	a *hi.Assignment,
	src data.Source,
	eng expression.Engine,
) interactor.ResolvedSlot {
	if a == nil {
		return interactor.ResolvedSlot{}
	}

	return interactor.ResolvedSlot{
		Declared: true,
		IDs:      a.Resolve(ctx, src, eng),
	}
}

// Authorize reports whether actor may act on the task, per ADR-020 §2.5: if an
// assignee is declared, only a matching UserID is authorized (the restrictive
// gate); otherwise a matching candidateUser OR an intersecting candidateGroup
// authorizes; a task with no triad member declared is open to any actor. A
// failed/empty expression resolves to an empty set, i.e. denies. A nil verdict
// means authorized; a non-nil error is a non-terminal denial (the caller keeps
// the task parked and waits for the right actor).
//
// It resolves the triad and applies the verdict through interactor.Eligibility, so
// the rule and its denial error have a single author (SRD-073 FR-5b/FR-5d). The
// engine's own checks read a snapshot resolved at distribution; this method stays
// as the entry point for an embedder wanting to pre-flight an actor against a task
// (SRD-073 §4.6).
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
			errs.D(observability.AttrTaskID, ut.ID()))
	}

	return ut.ResolveEligibility(ctx, src, eng).Authorize(ut.ID(), actor)
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
				errs.D(observability.AttrTaskID, ut.ID()))
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
					errs.D(observability.AttrTaskID, ut.ID()),
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
				errs.D(observability.AttrTaskID, ut.ID()),
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
			errs.D(observability.AttrTaskID, taskID),
			errs.D("output", name))
	}

	return nil
}
