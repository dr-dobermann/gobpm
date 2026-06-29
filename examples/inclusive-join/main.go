// Command inclusive-join demonstrates the Inclusive (OR) gateway diamond: the
// split forks the subset of branches whose condition is true (ADR-005 §2.9) and
// the converging OR-join waits for exactly that subset before continuing once
// (§2.10) — a never-taken branch is found unreachable and does not stall it.
//
//	start → OR-split ─┬ >1000 → manager-review ┐
//	                  ├ >500  → fraud-check     ┼→ OR-join → finalize → end
//	                  └ <100  → fast-track      ┘
//
// The process build lives in process.go; this file is the engine wiring + run.
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
  inclusive-join:
    start → OR-split ─┬ >1000 → manager-review ┐
                      ├ >500  → fraud-check     ┼→ OR-join → finalize → end
                      └ <100  → fast-track      ┘

`)
	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	engine, err := thresher.New("inclusive-join-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	const amount = 1500 // > 1000 and > 500 → two branches fork; fast-track is not

	proc, err := buildProcess(amount)
	if err != nil {
		return fmt.Errorf("build process: %w", err)
	}

	if _, err := engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	fmt.Printf("order amount = %d\n", amount)

	h, err := engine.StartLatest(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	fmt.Printf("✓ inclusive-join completed (%s): the OR-join merged the active "+
		"branches and fired once\n", state)

	return nil
}
