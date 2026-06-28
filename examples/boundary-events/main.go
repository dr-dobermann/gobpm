// Command boundary-events demonstrates an interrupting boundary event as a timeout
// on a long-running activity (SRD-029 / ADR-018): a 2s timer boundary attached to a
// ~4s payment ServiceTask fires first, interrupts the activity mid-execution, and
// routes the token onto the boundary's exception flow.
//
//	start → [process-payment] ───────────────> end-paid
//	             ╳ (timer boundary, 2s, interrupting)
//	             └─> [cancel-order] ─────────> end-cancelled
//
// The process build lives in process.go, the operations in handlers.go; this file is
// the engine wiring + run.
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
  boundary-events:
    start → [process-payment] ───────────────> end-paid
                 ╳ (timer boundary, 2s, interrupting)
                 └─> [cancel-order] ─────────> end-cancelled

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	engine, err := thresher.New("boundary-events-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	proc, err := buildProcess()
	if err != nil {
		return fmt.Errorf("build process: %w", err)
	}

	if err := engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	h, err := engine.StartProcess(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	fmt.Printf("\n✓ boundary-events completed (%s): the 2s timer boundary fired "+
		"before the 4s payment finished — it interrupted the activity and routed "+
		"to cancel-order\n", state)

	return nil
}
