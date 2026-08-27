package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// launchBound is the happy path: the host supplies the required "amount"
// (and the optional "currency"), the instance runs to completion, and the
// declared result is read back from the handle — total from the child,
// started_at from the engine's runtime variable.
func launchBound(
	ctx context.Context, engine *thresher.Thresher, quote string, got *seen,
) error {
	h, err := engine.StartLatest(quote,
		thresher.WithStartInput("amount", amount),
		thresher.WithStartInput("currency", "EUR"))
	if err != nil {
		return fmt.Errorf("start quote: %w", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("wait completion: %w", err)
	}

	if err := got.check(wantTotal); err != nil {
		return fmt.Errorf("call contract: %w", err)
	}

	outs := h.Outputs()
	for _, d := range outs {
		fmt.Printf("  ← output %s = %v\n", d.Name(),
			d.Value().Get(context.Background()))
	}

	if err := checkOutputs(outs, wantTotal); err != nil {
		return fmt.Errorf("process outputs: %w", err)
	}

	fmt.Printf("  ✓ completed (%s)\n\n", state)

	return nil
}

// launchRefused shows the boundary holding: with a contract declared, a
// launch that leaves a required input unbound, or delivers a datum the
// contract does not name, is refused BEFORE the instance exists — the
// process never waits for data that was not supplied (ADR-040 §2.2).
func launchRefused(engine *thresher.Thresher, quote string) error {
	_, err := engine.StartLatest(quote)
	if err == nil {
		return fmt.Errorf("a launch without %q was accepted", "amount")
	}

	fmt.Printf("  ✗ refused (no amount): %v\n\n", err)

	_, err = engine.StartLatest(quote,
		thresher.WithStartInput("amount", amount),
		thresher.WithStartInput("discount", 5))
	if err == nil {
		return fmt.Errorf("a launch with the undeclared %q was accepted",
			"discount")
	}

	fmt.Printf("  ✗ refused (undeclared discount): %v\n", err)

	return nil
}
