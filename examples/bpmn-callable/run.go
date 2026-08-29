package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// startAmount is what the host binds into the caller at launch; the imported
// callable adds a fifth of it as tax and hands "total" back.
const startAmount = 100.0

// runQuote starts the imported caller and checks BOTH calls did their work.
//
// Completing is not enough to prove anything here. A call that resolved to
// the wrong process, or a callable whose contract the engine could not fill,
// can still leave a completed instance behind — so the value the callable
// produced is read back out and checked, which is the only evidence the
// reference reached the right definition.
func runQuote(ctx context.Context, engine *thresher.Thresher) error {
	h, err := engine.StartLatest("quote",
		thresher.WithStartInput("amount", startAmount))
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

	// The caller's own declared output, which only exists because the
	// callable produced it and the direct mapping carried it back. This is
	// the half that was unreachable from a document until the call activity
	// was allowed to say what it passes.
	total, err := h.Data().GetData("total")
	if err != nil {
		return fmt.Errorf("read total: %w — the callable's declared output "+
			"should have crossed back into the caller", err)
	}

	got, err := data.As[float64](ctx, total.Value())
	if err != nil {
		return fmt.Errorf("total is not a number: %w", err)
	}

	if want := startAmount + startAmount/5; got != want {
		return fmt.Errorf("total = %v, want %v — the call reached a "+
			"different callable than the reference named", got, want)
	}

	// And WHICH definitions the three references reached. A completed instance proves the tokens moved; these counters
	// prove they moved through the callables the document named.
	if got := taxRuns.Load(); got != 2 {
		return fmt.Errorf("the imported callable ran %d times, want 2 — "+
			"the bare and the self-qualified reference both name it", got)
	}

	if got := auditRuns.Load(); got != 1 {
		return fmt.Errorf("the resolver-mapped callable ran %d times, want "+
			"1 — the qualified reference did not reach shared.audit", got)
	}

	fmt.Printf("\n  data crossed the call boundary: %v + tax = %v\n",
		startAmount, got)
	fmt.Print("  the unqualified call ran the imported callable\n")
	fmt.Print("  the self-qualified call collapsed to the same key and ran " +
		"it again\n")
	fmt.Print("  the qualified call reached shared.audit through the " +
		"resolver\n")
	fmt.Print("\n✓ bpmn-callable completed: reuse by reference, across a " +
		"document boundary\n")

	return nil
}

// showUnresolvable runs the imported caller on an engine with NO resolver, so
// the default's refusal is visible rather than described.
//
// It is the more instructive half of the seam. The engine cannot invent an
// answer for "audit in namespace .../shared" — taking the local part would
// call whatever happens to share the name — so it refuses, and the refusal
// names both the namespace it could not map and the option that maps it. A
// failed call is a technical fault, so it lands as an incident an operator
// retries once the cause is fixed, not as a dead instance.
func showUnresolvable(ctx context.Context, procs []*process.Process) error {
	engine, err := thresher.New("bpmn-callable-unresolved",
		thresher.WithoutBanner(),
		thresher.WithoutStartupConfig(),
		thresher.WithRuleEngine(decisions()))
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	for _, p := range procs {
		if _, err = engine.RegisterProcess(p); err != nil {
			return fmt.Errorf("register %q: %w", p.ID(), err)
		}
	}

	if err = engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	h, err := engine.StartLatest("quote",
		thresher.WithStartInput("amount", startAmount))
	if err != nil {
		return fmt.Errorf("start quote: %w", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for h.OpenIncidents() == 0 {
		if time.Now().After(deadline) {
			return fmt.Errorf("the qualified call did not fault: with no " +
				"resolver the engine has no key for it, and guessing one " +
				"is exactly what it must not do")
		}

		time.Sleep(10 * time.Millisecond)
	}

	incidents := h.Incidents()
	if len(incidents) != 1 {
		return fmt.Errorf("incidents = %d, want 1", len(incidents))
	}

	fmt.Printf("\n  with no resolver, the qualified call faults:\n    %s\n\n",
		rootMessage(incidents[0].Cause))

	// This run got as far as the FIRST call before faulting on the third, so
	// it has already moved the counters. They belong to the run being
	// asserted, not to this demonstration.
	taxRuns.Store(0)
	auditRuns.Store(0)

	return engine.Shutdown(context.Background())
}

// rootMessage digs a classified error down to its INNERMOST message.
//
// The outer wrapper says which call failed; the inner one says what a host has
// to do about it, which is the half worth printing.
func rootMessage(s string) string {
	msg := strings.TrimSpace(s)

	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "Message:") {
			msg = strings.TrimSpace(strings.TrimPrefix(t, "Message:"))
		}
	}

	return msg
}
