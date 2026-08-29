// Command association-expressions demonstrates BPMN's two expression shapes
// on a data association (§10.4.2 rules 1 and 2, ADR-011 §2.4, SRD-097): a
// TRANSFORMATION whose result replaces the target, and an ASSIGNMENT that
// writes at a path inside it.
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
	fmt.Print(banner)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	engine, err := thresher.New("association-expressions-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	proc, err := buildProcess()
	if err != nil {
		return fmt.Errorf("build process: %w", err)
	}

	if _, err = engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err = engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	h, err := engine.StartLatest(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	if err := engine.Shutdown(context.Background()); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	fmt.Printf("\n✓ association-expressions completed (%s)\n", state)

	return nil
}
