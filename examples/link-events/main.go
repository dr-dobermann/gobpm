// Command link-events demonstrates Link events — an intra-process GOTO
// (ADR-006 v.4 §2.8, SRD-057). A Link is a static, name-paired connector: a
// source Intermediate Throw hands the token to the same-name target
// Intermediate Catch within one Process level. It is not a wait — there is no
// broadcast, no correlation, no subscription; the throw simply redirects.
//
// This example builds an ON-PAGE LOOP with a Link, the canonical use: instead
// of a long sequence-flow line looping back, an initial throw"repeat" and a
// back-edge throw"repeat" both redirect to one catch"repeat", so the work task
// runs each iteration until a data condition exits (§10.5.1 "looping
// situations"). It shows the many-sources → one-target pairing and re-entrancy.
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
  link-events (an on-page loop by static name-pairing):
    start → throw"repeat"            (initial jump into the loop)
    catch"repeat" → work → XOR{ count<3 → throw"repeat" | done → end }

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	count := 0

	proc, err := buildProcess(&count)
	if err != nil {
		return fmt.Errorf("build process: %w", err)
	}

	engine, err := thresher.New("link-events-engine",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig())
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
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

	fmt.Printf("  ✓ completed (%s) after %d iterations via the Link\n",
		state, count)

	return nil
}
