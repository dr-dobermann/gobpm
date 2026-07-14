// Command structural-output-mapping demonstrates output-mapping assembly-by-head
// (ADR-011 v.6 §2.9.5, SRD-043): a worker returns a FLAT body, and a task's
// structural output-mapping rules assemble it into one nested «order» record —
// a field plus an auto-vivified items list — which a downstream task reads back
// by path. See README.md for the walk-through.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/tasks/localdispatcher"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print(`
  structural-output-mapping:
    start → quote (assemble order.* from a flat body) → read-back → end

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	disp := localdispatcher.New(nil, 0)
	if err := disp.RegisterWorker(ctx, "quote", quoteWorker()); err != nil {
		return fmt.Errorf("register quote worker: %w", err)
	}

	engine, err := thresher.New("assembly-engine",
		thresher.WithWorkerDispatcher(disp),
		thresher.WithoutBanner(), thresher.WithoutStartupConfig())
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

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

	fmt.Printf("  ✓ completed (%s)\n", state)

	return nil
}
