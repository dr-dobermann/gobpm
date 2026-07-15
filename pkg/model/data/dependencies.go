package data

// DependencyLister is the optional capability of a FormalExpression to
// declare the data paths (structural grammar — "order", "order.items[0]")
// its evaluation reads.
//
// The contract is the conditional-events granularity rule (ADR-006 v.3
// §2.7): an expression that does NOT carry the capability — or returns a
// nil list — may read anything, so its conditional subscription re-evaluates
// on every non-empty commit (the safe fallback: a missing statement costs
// performance, never correctness). A non-empty list narrows re-evaluation
// to commits whose changed paths overlap a declared one (PathsOverlap). An
// empty non-nil list is never legal — it would mean "never re-evaluate" —
// and declaring constructors reject it (goexpr.WithDependencies).
type DependencyLister interface {
	// Dependencies returns the declared read paths, or nil when the
	// expression declared nothing.
	Dependencies() []string
}
