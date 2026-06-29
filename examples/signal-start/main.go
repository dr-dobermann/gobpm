package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print(`
  signal-start (a broadcast signal CREATES instances — no StartProcess):
    fulfillment:  ◉signal(order-received) → handle-order  → end
    audit:        ◉signal(order-received) → record-audit  → end

    One broadcast of "order-received" instantiates BOTH processes
    (broadcast, not point-to-point).

`)
	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	engine, err := thresher.New("signal-start-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	fulfillment, err := signalStartProcess(
		"fulfillment", "  order-received → handling fulfillment")
	if err != nil {
		return fmt.Errorf("build fulfillment: %w", err)
	}

	audit, err := signalStartProcess(
		"audit", "  order-received → recording audit")
	if err != nil {
		return fmt.Errorf("build audit: %w", err)
	}

	for _, p := range []*process.Process{fulfillment, audit} {
		if _, err := engine.RegisterProcess(p); err != nil {
			return fmt.Errorf("register %s: %w", p.ID(), err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	def, err := signalDef(signalName)
	if err != nil {
		return err
	}

	fmt.Println(`  ▶ broadcasting "order-received" once (no StartProcess call)...`)

	if err := engine.PropagateEvent(ctx, def); err != nil {
		return fmt.Errorf("broadcast signal: %w", err)
	}

	if err := awaitAll(ctx, engine, 2); err != nil {
		return err
	}

	fmt.Println("✓ one broadcast signal created and completed both instances")

	return nil
}
