package hinteraction

// RoleKind identifies which level of BPMN's ResourceRole chain a role occupies.
//
// The chain PotentialOwner → HumanPerformer → Performer → ResourceRole adds no
// attributes at any level — §10.3.4.1 states that the HumanPerformer element
// "inherits the attributes and model associations of ResourceRole (see Table
// 10.5), through its relationship to Performer, but does not have any additional
// attributes or model associations" — so the whole hierarchy carries exactly one
// bit of information: which role this is. The kind is that bit, which is why
// gobpm models the chain as a discriminator rather than as four types that would
// differ in no field (ADR-020 v.3 §2.5.4).
type RoleKind string

const (
	// RoleResource is a bare ResourceRole: a resource associated with the
	// activity, human or not. Declarative — it grants no authorization.
	RoleResource RoleKind = "ResourceRole"

	// RolePerformer is BPMN 1.2's generic performer — "the resource that will
	// perform or will be responsible for the Activity" (Table 10.3), which the
	// standard does not restrict to people. Declarative.
	RolePerformer RoleKind = "Performer"

	// RoleHumanPerformer is the human specialization BPMN 2.0 added beside the
	// generic Performer, "allowing specifying more specific human roles"
	// (§10.3.4.1). It grants authorization.
	RoleHumanPerformer RoleKind = "HumanPerformer"

	// RolePotentialOwner names the "persons who can claim and work on" a User
	// Task, one of whom "becomes the actual owner of a Task, usually by
	// explicitly claiming it" (§10.3.4.1). It grants authorization.
	RolePotentialOwner RoleKind = "PotentialOwner"
)

// authorizingKinds are the role kinds that contribute human eligibility.
//
// A bare ResourceRole or a Performer may name a machine, a system or an
// organization — which is precisely why BPMN 2.0 introduced HumanPerformer
// beside the generic role. Treating either as a grant of human authorization
// would read a claim into the standard that §10.3.4.1's own rationale denies,
// and would do so in the permissive direction.
var authorizingKinds = map[RoleKind]bool{
	RoleHumanPerformer: true,
	RolePotentialOwner: true,
}

// Authorizes reports whether a role of this kind contributes to a task's
// eligible set (ADR-020 v.3 §2.5.4). An unknown kind authorizes nothing.
func (k RoleKind) Authorizes() bool {
	return authorizingKinds[k]
}
