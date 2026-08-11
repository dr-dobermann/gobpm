// Command compensation-events demonstrates BPMN compensation (SRD-059,
// ADR-026): undoing work a completed activity already did, driven by a
// compensation throw that fires each registered handler in reverse order.
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
  compensation-events (a trip-booking saga):
    start → [book-hotel] → [book-flight] → cancel-trip (Compensation End)
                ╳ undo-hotel   ╳ undo-flight
    reverse completion order: undo-flight runs BEFORE undo-hotel

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	engine, err := thresher.New("compensation-events-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	// Records the execution order, so the reverse-order claim is checked.
	runLog := newRunLog()

	proc, err := buildProcess(runLog)
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

	// Reverse order is the claim: hotel and flight are booked forwards, and
	// the Compensation End Event must undo the LAST completed activity first.
	if err := runLog.check(
		"book-hotel", "book-flight", "undo-flight", "undo-hotel",
	); err != nil {
		return fmt.Errorf("compensation order: %w", err)
	}

	fmt.Printf("\n✓ compensation-events completed (%s): both bookings entered "+
		"the completion ledger; the Compensation End Event undid them in "+
		"reverse order and waited for both handlers\n", state)

	return nil
}
