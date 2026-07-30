package interactor

import (
	"slices"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
)

// errorClass identifies errors raised by the human-task boundary.
const errorClass = "INTERACTOR_ERRORS"

// ResolvedSlot is one triad member resolved to identifiers, keeping *whether the
// model declared it* distinct from *what it resolved to*. The distinction is
// load-bearing: a declared slot that resolves to nothing authorizes no one (a
// failed resource query is an empty result set, not an open task), whereas an
// undeclared slot is simply absent from the verdict.
type ResolvedSlot struct {
	// IDs are the identifiers the member resolved to — user ids for the assignee
	// and candidate-user slots, group ids for the candidate-group slot. Empty on
	// a declared slot means "resolved to nobody".
	IDs []string

	// Declared reports that the UserTask carries this triad member at all.
	Declared bool
}

// Eligibility is a UserTask's assignment triad resolved to identifier sets when
// the task was distributed (ADR-020 v.2 §2.7). It is the frozen input to every
// later authorization check: it never re-resolves, so an actor's right to act
// cannot be revoked by an unrelated data change while the task waits, and an
// owner cannot lose the ability to finish work it already holds.
//
// Being write-once and read-only after distribution, one value is safely shared
// by the instance's task registry and the engine-level one (SRD-073 FR-5a).
type Eligibility struct {
	Assignee        ResolvedSlot
	CandidateUsers  ResolvedSlot
	CandidateGroups ResolvedSlot
}

// DeniedEligibility returns an Eligibility that authorizes NOBODY — a declared
// assignee slot resolving to no one, which the verdict treats as the restrictive
// gate matching nothing.
//
// It is the fail-closed value for a triad that could not be resolved. The zero
// Eligibility must never be used for that: no slot declared reads as an OPEN task
// (BPMN's unspecified performer), so a failed resolution would silently authorize
// every actor. Failing closed leaves the task parked and uncompletable, which is
// visible and recoverable; failing open is neither.
func DeniedEligibility() Eligibility {
	return Eligibility{Assignee: ResolvedSlot{Declared: true}}
}

// Authorize reports whether actor may act on taskID, applying ADR-020 v.2 §2.5's
// verdict to the frozen sets:
//
//   - no triad member declared — the task is open to any actor (BPMN's
//     unspecified performer);
//   - an assignee declared — only a matching UserID is authorized, and the
//     candidate slots are not consulted at all (the restrictive gate);
//   - otherwise — a matching candidate user OR an intersecting candidate group.
//
// A nil error means authorized. A non-nil error is the NON-TERMINAL denial: the
// caller keeps the task parked and waits for the right actor. The denial is
// authored here so every caller — the UserTask, the instance loop, the engine's
// pre-hydration task gate — surfaces the identical error (SRD-073 FR-5d).
func (e Eligibility) Authorize(taskID string, actor hi.Actor) error {
	if taskID == "" {
		return errs.New(
			errs.M("Eligibility.Authorize: an empty task id isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if actor == nil {
		return errs.New(
			errs.M("Eligibility.Authorize: a nil Actor isn't allowed"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("task_id", taskID))
	}

	if e.permits(actor) {
		return nil
	}

	return errs.New(
		errs.M("actor %q is not authorized for user task %q",
			actor.UserID(), taskID),
		errs.C(errorClass, errs.ConditionFailed),
		errs.D("task_id", taskID),
		errs.D("user_id", actor.UserID()))
}

// Open reports a task no triad member was declared for — authorized for any
// actor. Exposed because a distributor may want to present an open task
// differently from one with a candidate list.
func (e Eligibility) Open() bool {
	return !e.Assignee.Declared &&
		!e.CandidateUsers.Declared &&
		!e.CandidateGroups.Declared
}

// permits is the membership predicate Authorize wraps. Unexported so the denial
// error has exactly one author.
func (e Eligibility) permits(actor hi.Actor) bool {
	if e.Open() {
		return true
	}

	// A declared assignee is the sole gate — an empty resolution denies rather
	// than falling through to the candidate slots.
	if e.Assignee.Declared {
		return slices.Contains(e.Assignee.IDs, actor.UserID())
	}

	if e.CandidateUsers.Declared &&
		slices.Contains(e.CandidateUsers.IDs, actor.UserID()) {
		return true
	}

	return e.CandidateGroups.Declared &&
		intersects(e.CandidateGroups.IDs, actor.Groups())
}

// intersects reports whether a and b share at least one element.
func intersects(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}

	return false
}
