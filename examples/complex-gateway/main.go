// Command complex-gateway demonstrates the Complex gateway (ADR-005 §2.11) as a
// data-aware partial join: three approvers run in parallel and the Complex join
// fires on the rule [(amount<1000, 2), (amount>=1000, 3)] — two approvals for a
// small order, all three for a large one — consuming any later approval as a
// trailing token.
//
//	start → AND-split ─┬→ manager ─┐
//	                   ├→ finance ─┼ Complex join → finalize → end
//	                   └→ cfo ─────┘
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
  complex-gateway:
    start → AND-split ─┬→ manager ─┐
                       ├→ finance ─┼ Complex join → finalize → end
                       └→ cfo ─────┘

`)
	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	engine, err := thresher.New("complex-gateway-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	const amount = 500 // < 1000 → two of the three approvals are enough

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

	fmt.Printf("order amount = %d (needs 2 approvals)\n", amount)

	h, err := engine.StartProcess(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	fmt.Printf("✓ complex-gateway completed (%s): the join fired on the 2nd "+
		"approval; the 3rd was consumed as a trailing token\n", state)

	return nil
}
