// Command call-activity demonstrates the Call Activity (ADR-023, SRD-050): a
// caller process invokes a SEPARATELY registered process as a CHILD instance —
// the reuse boundary. The declared Input/Output parameters are the call
// contract: "subtotal" crosses in (cloned), the child computes, and "total"
// crosses back into the caller's scope. Unlike the embedded Sub-Process (a
// nested scope in the same instance), the callee runs as its own isolated
// instance. See README.md.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print(`
  call-activity:
    checkout: start → charge[calls "tax-calc"] → show → end
    tax-calc: start → tax → end   (a reusable child process)

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	engine, err := thresher.New("call-activity-engine",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig())
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	sub := engine.Observe(callPrinter{})
	defer sub.Cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	callee, err := buildCallee()
	if err != nil {
		return fmt.Errorf("build callee: %w", err)
	}

	caller, err := buildCaller(100)
	if err != nil {
		return fmt.Errorf("build caller: %w", err)
	}

	if _, err := engine.RegisterProcess(callee); err != nil {
		return fmt.Errorf("register callee: %w", err)
	}

	if _, err := engine.RegisterProcess(caller); err != nil {
		return fmt.Errorf("register caller: %w", err)
	}

	h, err := engine.StartLatest(caller.ID())
	if err != nil {
		return fmt.Errorf("start caller: %w", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("wait completion: %w", err)
	}

	sub.Cancel()

	fmt.Printf("  ✓ completed (%s)\n", state)

	return nil
}
