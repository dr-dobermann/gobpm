package renv

import "context"

// Dehydratable is an optional wait-node capability (ADR-007 v.2 §2.4,
// SRD-071): a wait node declares whether parking here should release the
// instance's goroutines (dehydration) — a node that does not implement it is
// non-dehydratable and keeps the instance resident. The decision is the
// element's own and may be data-driven: a Timer weighs its deadline against a
// threshold; a UserTask always releases; an external-worker ServiceTask never
// does (a job in flight is active work); an Event-Based Gateway always releases
// (it is the wait node, a pure wait race, and ignores its arm events' policies).
type Dehydratable interface {
	// Dehydratable reports whether this wait node releases the instance's
	// goroutines when parked here. re gives access to the runtime (clock,
	// data, the configured dehydration threshold) for a data-driven decision.
	Dehydratable(ctx context.Context, re RuntimeEnvironment) bool
}
