// Command structural-data demonstrates reaching INTO a structural value by path
// (ADR-011 v.6 §2.9). The "order" property is a record — {id, total, items:[…]}
// — and both a service task and a gateway condition address into it: the task
// reads order.items[0].price through the narrow DataReader, and the exclusive
// gateway routes on order.total.
//
//	start ─> read order.items[0].price ─> XOR ─┬─ order.total > 100 ─> premium ─> end
//	                                           └─ default            ─> standard ─> end
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
	fmt.Print("\n  structural-data: reaching into a record value by path\n\n")

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	engine, err := thresher.New("structural-data-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	const total = 150

	// Records which lane the gateway took, so the routing claim is checked.
	ran := newPathSet()

	proc, err := buildProcess(ran, total)
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

	fmt.Printf("order.total = %d\n", total)

	h, err := engine.StartLatest(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	// The gateway condition reaches into the same record by path, so which
	// lane ran is the second half of the demonstration: a path that resolved
	// to nothing would evaluate false, take the default flow, and complete
	// exactly as happily.
	if err := ran.check(
		[]string{"premium"}, []string{"standard"},
	); err != nil {
		return fmt.Errorf("structural routing: %w", err)
	}

	fmt.Printf("✓ structural-data completed (%s)\n", state)

	return nil
}
