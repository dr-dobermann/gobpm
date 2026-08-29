package instance

import (
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
)

// The frozen-eligibility record (ADR-020 §2.7, SRD-090.D FR-10).
//
// Eligibility is assessed ONCE, when the iteration's task is announced, in the
// data context of that iteration — and a restore cannot recompute it. The
// element the iteration was seeded with is frame-local to an execution that no
// longer exists, so a re-resolution reads the host's scope instead, and a
// performer expression naming "the reviewer this one is for" resolves to
// nobody. Everyone holding that task in their inbox is then locked out of it.
//
// So the verdict rides the checkpoint beside the identity it was announced
// under, and comes back as it was.

// freezeEligibility renders a resolved triad for the checkpoint.
func freezeEligibility(e interactor.Eligibility) *checkpoint.TaskEligibility {
	return &checkpoint.TaskEligibility{
		Assignee:                append([]string{}, e.Assignee.IDs...),
		CandidateUsers:          append([]string{}, e.CandidateUsers.IDs...),
		CandidateGroups:         append([]string{}, e.CandidateGroups.IDs...),
		Roles:                   append([]string{}, e.Roles.IDs...),
		RolesDeclared:           e.Roles.Declared,
		AssigneeDeclared:        e.Assignee.Declared,
		CandidateUsersDeclared:  e.CandidateUsers.Declared,
		CandidateGroupsDeclared: e.CandidateGroups.Declared,
	}
}

// thawEligibility rebuilds the verdict a checkpoint recorded.
func thawEligibility(r *checkpoint.TaskEligibility) interactor.Eligibility {
	return interactor.Eligibility{
		Assignee: interactor.ResolvedSlot{
			IDs: r.Assignee, Declared: r.AssigneeDeclared,
		},
		CandidateUsers: interactor.ResolvedSlot{
			IDs: r.CandidateUsers, Declared: r.CandidateUsersDeclared,
		},
		CandidateGroups: interactor.ResolvedSlot{
			IDs: r.CandidateGroups, Declared: r.CandidateGroupsDeclared,
		},
		Roles: interactor.ResolvedSlot{
			IDs: r.Roles, Declared: r.RolesDeclared,
		},
	}
}
