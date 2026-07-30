package interactor_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/stretchr/testify/require"
)

// fakeActor is a test hi.Actor.
type fakeActor struct {
	id     string
	groups []string
}

func (a fakeActor) UserID() string   { return a.id }
func (a fakeActor) Groups() []string { return a.groups }

// declared builds a slot the model carries, resolved to ids.
func declared(ids ...string) interactor.ResolvedSlot {
	return interactor.ResolvedSlot{Declared: true, IDs: ids}
}

// TestEligibilityAuthorize covers the ADR-020 v.2 §2.5 verdict over the frozen
// triad — the truth table UserTask.Authorize enforced before SRD-073 moved the
// rule here (V1).
func TestEligibilityAuthorize(t *testing.T) {
	tests := []struct {
		name       string
		eligible   interactor.Eligibility
		actor      fakeActor
		authorized bool
	}{
		{
			name:       "no slot declared authorizes anyone",
			eligible:   interactor.Eligibility{},
			actor:      fakeActor{id: "anybody"},
			authorized: true,
		},
		{
			name:       "assignee match authorizes",
			eligible:   interactor.Eligibility{Assignee: declared("john")},
			actor:      fakeActor{id: "john"},
			authorized: true,
		},
		{
			name:       "assignee mismatch denies",
			eligible:   interactor.Eligibility{Assignee: declared("john")},
			actor:      fakeActor{id: "mary"},
			authorized: false,
		},
		{
			name: "declared assignee overrides candidate slots",
			eligible: interactor.Eligibility{
				Assignee:        declared("john"),
				CandidateUsers:  declared("mary"),
				CandidateGroups: declared("reviewers"),
			},
			actor:      fakeActor{id: "mary", groups: []string{"reviewers"}},
			authorized: false,
		},
		{
			name: "declared assignee resolved to nobody denies, never falls through",
			eligible: interactor.Eligibility{
				Assignee:       declared(),
				CandidateUsers: declared("mary"),
			},
			actor:      fakeActor{id: "mary"},
			authorized: false,
		},
		{
			name:       "candidate user match authorizes",
			eligible:   interactor.Eligibility{CandidateUsers: declared("a", "b")},
			actor:      fakeActor{id: "b"},
			authorized: true,
		},
		{
			name:       "candidate user mismatch denies",
			eligible:   interactor.Eligibility{CandidateUsers: declared("a", "b")},
			actor:      fakeActor{id: "c"},
			authorized: false,
		},
		{
			name:       "candidate group intersection authorizes",
			eligible:   interactor.Eligibility{CandidateGroups: declared("g1", "g2")},
			actor:      fakeActor{id: "x", groups: []string{"g9", "g2"}},
			authorized: true,
		},
		{
			name:       "candidate group without intersection denies",
			eligible:   interactor.Eligibility{CandidateGroups: declared("g1", "g2")},
			actor:      fakeActor{id: "x", groups: []string{"g9"}},
			authorized: false,
		},
		{
			name: "candidate slots are OR-ed, not AND-ed",
			eligible: interactor.Eligibility{
				CandidateUsers:  declared("mary"),
				CandidateGroups: declared("reviewers"),
			},
			actor:      fakeActor{id: "mary", groups: []string{"nothing"}},
			authorized: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.eligible.Authorize("task-1", tt.actor)

			if tt.authorized {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)
		})
	}
}

// TestEligibilityDenialCarriesTaskAndUser checks the denial an embedder observes:
// the non-terminal ConditionFailed class plus the task and user that were refused,
// and never the task's data (V1a).
func TestEligibilityDenialCarriesTaskAndUser(t *testing.T) {
	err := interactor.Eligibility{Assignee: declared("john")}.
		Authorize("task-7", fakeActor{id: "mary"})

	require.Error(t, err)

	var aerr *errs.ApplicationError
	require.ErrorAs(t, err, &aerr)
	require.Contains(t, aerr.Classes, errs.ConditionFailed)
	require.Equal(t, "task-7", aerr.Details["task_id"])
	require.Equal(t, "mary", aerr.Details["user_id"])
}

// TestEligibilityOpen distinguishes an open task from one that merely resolved to
// nobody — the difference that makes DeniedEligibility necessary.
func TestEligibilityOpen(t *testing.T) {
	require.True(t, interactor.Eligibility{}.Open())
	require.False(t, interactor.DeniedEligibility().Open())
	require.False(t,
		interactor.Eligibility{CandidateUsers: declared("a")}.Open())
}

// TestDeniedEligibilityAuthorizesNobody pins the fail-closed value: a triad that
// could not be resolved must refuse every actor, never read as an open task.
func TestDeniedEligibilityAuthorizesNobody(t *testing.T) {
	denied := interactor.DeniedEligibility()

	for _, a := range []fakeActor{
		{id: "john"},
		{id: ""},
		{id: "x", groups: []string{"admins", "reviewers"}},
	} {
		require.Error(t, denied.Authorize("task-1", a))
	}
}

// TestEligibilityAuthorizeValidatesParameters covers the public-API guards (V1b).
func TestEligibilityAuthorizeValidatesParameters(t *testing.T) {
	open := interactor.Eligibility{}

	t.Run("empty task id", func(t *testing.T) {
		require.Error(t, open.Authorize("", fakeActor{id: "john"}))
	})

	t.Run("nil actor", func(t *testing.T) {
		require.Error(t, open.Authorize("task-1", nil))
	})
}
