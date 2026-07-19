package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// compositeIterator drives a looped composite activity (a Sub-Process host) that
// re-opens its child scope for each pass (ADR-025 §2.2). Standard Loop and
// Multi-Instance are the two strategies over this one re-entry mechanism; the
// re-entry seam (onScopeOpen / resumeScopeHost) calls the interface instead of
// branching on the concrete iteration kind.
type compositeIterator interface {
	// firstOpen prepares the first pass and reports whether the body scope
	// opens at all. open=false means zero iterations — the host resumes without
	// opening a scope. A non-nil error aborts the instance.
	firstOpen(ctx context.Context, ls *loopState, host *track, node flow.Node) (open bool, err error)

	// afterDrain runs when the child scope drains; reopen=true re-opens it for
	// another pass, false completes the activity. A non-nil error aborts the
	// instance.
	afterDrain(ctx context.Context, ls *loopState, host *track, node flow.Node) (reopen bool, err error)
}

// compositeIteratorOf returns the iteration strategy a composite host runs
// under — Standard Loop or Multi-Instance — or nil when the host runs once. The
// two markers are mutually exclusive by construction (§13.3.6 vs §13.3.7), so
// the order of the checks is immaterial.
func compositeIteratorOf(node flow.Node) compositeIterator {
	if sl := standardLoopOf(node); sl != nil {
		return standardLoopIterator{sl: sl}
	}

	if mi := multiInstanceOf(node); mi != nil {
		return miIterator{mi: mi}
	}

	return nil
}
