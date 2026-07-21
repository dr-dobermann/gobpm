package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// compositeIterator drives a loop-goroutine-driven composite activity (a
// Sub-Process host) that re-opens its child scope for each pass (ADR-025 §2.2).
// Since ADR-025 v.2 §2.12 only sequential Multi-Instance rides this seam —
// Standard Loop drives itself off the loop through the iteration decorator
// (runCompositeLoop), so it no longer needs the loop-side firstOpen/afterDrain
// callbacks. The re-entry seam (onScopeOpen / resumeScopeHost) calls the interface
// instead of branching on the concrete iteration kind.
type compositeIterator interface {
	// firstOpen prepares the first pass and reports whether the body scope
	// opens at all. open=false means zero iterations — the host resumes without
	// opening a scope. A non-nil error aborts the instance.
	firstOpen(ctx context.Context, ls *loopState, host *track, node flow.Node) (open bool, err error)

	// beforeClose runs just before the drained child scope closes — the last
	// point its data is readable. A Multi-Instance captures the per-instance
	// output here. A non-nil error aborts the instance.
	beforeClose(ctx context.Context, host *track, childPath scope.DataPath) error

	// afterDrain runs when the child scope drains; reopen=true re-opens it for
	// another pass, false completes the activity. A non-nil error aborts the
	// instance.
	afterDrain(ctx context.Context, ls *loopState, host *track, node flow.Node) (reopen bool, err error)
}

// compositeIteratorOf returns the iteration strategy a loop-driven composite host
// runs under, or nil when the host runs once or drives itself. Only a SEQUENTIAL
// Multi-Instance rides the serial seam: a Standard Loop is decorator-driven
// (§2.12) and a parallel MI has its own control flow (open N scopes, complete on
// the last — SRD-056.A), so both return nil here, keeping every serial-seam site
// (onScopeOpen / resumeScopeHost / scopeLoopCounter) clear of them.
func compositeIteratorOf(node flow.Node) compositeIterator {
	if mi := multiInstanceOf(node); mi != nil && mi.IsSequential() {
		return miIterator{mi: mi}
	}

	return nil
}
