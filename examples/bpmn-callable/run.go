package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// runQuote starts the imported caller and checks BOTH calls did their work.
//
// Completing is not enough to prove anything here. A call that resolved to
// the wrong process, or a callable whose contract the engine could not fill,
// can still leave a completed instance behind — so the value the callable
// produced is read back out and checked, which is the only evidence the
// reference reached the right definition.
func runQuote(ctx context.Context, engine *thresher.Thresher) error {
	h, err := engine.StartLatest("quote")
	if err != nil {
		return fmt.Errorf("start quote: %w", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	if state != thresher.StateCompleted {
		return fmt.Errorf("quote finished %s, want %s",
			state, thresher.StateCompleted)
	}

	// The evidence that matters: WHICH definitions the three references
	// reached. A completed instance proves the tokens moved; these counters
	// prove they moved through the callables the document named.
	if got := taxRuns.Load(); got != 2 {
		return fmt.Errorf("the imported callable ran %d times, want 2 — "+
			"the bare and the self-qualified reference both name it", got)
	}

	if got := auditRuns.Load(); got != 1 {
		return fmt.Errorf("the resolver-mapped callable ran %d times, want "+
			"1 — the qualified reference did not reach shared.audit", got)
	}

	fmt.Print("\n  the unqualified call ran the imported callable\n")
	fmt.Print("  the self-qualified call collapsed to the same key and ran " +
		"it again\n")
	fmt.Print("  the qualified call reached shared.audit through the " +
		"resolver\n")
	fmt.Print("\n✓ bpmn-callable completed: reuse by reference, across a " +
		"document boundary\n")

	return nil
}
