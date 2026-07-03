package hinteraction

// Actor is the authenticated human acting on a task at runtime — the identity a
// TaskDistributor supplies and the engine authorizes against a UserTask's
// assignment triad (ADR-020 §2.6). It is deliberately distinct from the BPMN
// Performer element (a ResourceRole subtype, a role *declaration*): an Actor is
// the party *acting*, carrying exactly what the triad matches — a user id and the
// group ids the party belongs to.
type Actor interface {
	// UserID returns the actor's user identifier (matched against a task's
	// assignee and candidateUsers).
	UserID() string

	// Groups returns the group identifiers the actor belongs to (matched against
	// a task's candidateGroups).
	Groups() []string
}
