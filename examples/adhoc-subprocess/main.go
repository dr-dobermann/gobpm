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
  adhoc-subprocess (incident triage — order decided at runtime):
    start → (triage) → end
    triage holds gather-logs, notify-customer, escalate, close-incident
    with NO sequence flows between them; a Router answers what runs next.
    severity="high" forks notify-customer AND escalate, then the container
    waits for both and closes the incident.

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	engine, err := thresher.New("adhoc-subprocess-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	// Records the order the Router drove the activities in.
	log := newRunLog()

	proc, err := buildProcess(log, "high")
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

	// With severity=high the Router runs gather-logs first, then fans out to
	// notify-customer and escalate — whose relative order is genuinely up to
	// the scheduler — and closes only once both have settled. So the two
	// checkable claims are the boundaries, not the middle.
	if err := log.first("gather-logs"); err != nil {
		return fmt.Errorf("router order: %w", err)
	}

	if err := log.last("close-incident",
		"gather-logs", "notify-customer", "escalate"); err != nil {
		return fmt.Errorf("router order: %w", err)
	}

	fmt.Printf("\n✓ adhoc-subprocess completed (%s): the Router drove four "+
		"activities with no sequence flows, and the container ended when it "+
		"answered empty with nothing left running\n", state)

	return nil
}
