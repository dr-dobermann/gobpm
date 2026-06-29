// Command basic-process is the canonical GoBPM quick-start: a minimal
// Start → ServiceTask → End process where the ServiceTask runs YOUR Go code.
//
// The ServiceTask's work is a `gooper` functor — an ordinary Go function
// wrapped as the operation's implementation. Here it reads a process property
// and a runtime variable through its read-only DataReader, showing how a gofunc
// reaches process data without any message ceremony.
//
//	start ─> work (runs a Go functor) ─> end
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
  basic-process:
    start ─> work (runs a Go functor) ─> end

`)
	// Process properties instantiate with the standard data states.
	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	engine, err := thresher.New("basic-process-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	proc, err := buildProcess()
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

	h, err := engine.StartProcess(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	// Block until the instance finishes — the handle's completion signal
	// replaces the manual done channel and the grace sleep (SRD-018).
	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	fmt.Printf("✓ basic-process completed (%s): "+
		"start → service task (read property + RUNTIME var) → end\n", state)

	return nil
}
