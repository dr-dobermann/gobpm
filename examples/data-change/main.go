// Command data-change demonstrates commit-diff change detection (ADR-011 v.6
// §2.9.4, SRD-044): each node's frame commit is diffed against the prior
// committed value, and an observer receives one DataChange fact per changed
// path — a whole-value first commit is ONE Value_Added at its root, a nested
// re-commit ONE Value_Updated at the changed leaf. See README.md.
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
  data-change:
    start → produce (receipt={sum:5}) → reprice (receipt={sum:6}) → end

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	engine, err := thresher.New("data-change-engine",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig())
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	sub := engine.Observe(&dataChangePrinter{})
	defer sub.Cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	proc, err := buildProcess()
	if err != nil {
		return fmt.Errorf("build process: %w", err)
	}

	if _, err := engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	h, err := engine.StartLatest(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("wait completion: %w", err)
	}

	// Cancel drains the buffered facts so both DataChange lines land before
	// the final status prints.
	sub.Cancel()

	fmt.Printf("  ✓ completed (%s)\n", state)

	return nil
}
