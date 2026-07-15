// Command conditional-events demonstrates conditional events (ADR-006 v.3
// §2.7, SRD-048): an intermediate conditional catch parks a branch until a
// sibling task's committed change flips its condition false→true — data-driven
// waiting without polling. The condition declares its read paths
// (goexpr.WithDependencies), so only commits touching "total" re-evaluate it.
// See README.md.
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
  conditional-events:
    start → prep → { addItems (total=140)  → end-shop,
                     watch [total>100]     → notify → end-notify }

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	engine, err := thresher.New("conditional-events-engine",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig())
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	sub := engine.Observe(&condEventPrinter{})
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

	// Cancel drains the buffered facts so the fire line lands before the
	// final status prints.
	sub.Cancel()

	fmt.Printf("  ✓ completed (%s)\n", state)

	return nil
}
