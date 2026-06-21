// Command gateway-routing demonstrates data-based branching with an Exclusive
// (XOR) gateway: the order's "amount" property decides which single branch the
// token takes (ADR-005 §2.8 — first-true condition, else the default flow).
//
//	start ─> XOR ─┬─ amount > 1000 ─> manager review ─> end
//	              └─ default        ─> auto-approve   ─> end
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
  gateway-routing:
    start ─> XOR ─┬─ amount > 1000 ─> manager review ─> end
                  └─ default        ─> auto-approve   ─> end

`)
	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	engine, err := thresher.New("gateway-routing-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	const amount = 2500

	proc, err := buildProcess(amount)
	if err != nil {
		return fmt.Errorf("build process: %w", err)
	}

	if err := engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	fmt.Printf("order amount = %d\n", amount)

	h, err := engine.StartProcess(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	fmt.Printf("✓ gateway-routing completed (%s): the exclusive gateway chose "+
		"the branch by data\n", state)

	return nil
}
