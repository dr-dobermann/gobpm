// Command transaction-sub-process demonstrates a Transaction Sub-Process
// (SRD-061, ADR-028): a Sub-Process variant that aborts atomically on a Cancel
// End Event — the ACID-style all-or-nothing unit in BPMN form.
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
  transaction-sub-process (a booking Transaction that cancels):
    start → (booking) → end
              ⚡ cancel-bnd → notify-customer
    booking = reserve-seat → charge-card → cancel-booking (Cancel End)
                ╳ release-seat  ╳ refund-card
    the Cancel End aborts: refund-card runs BEFORE release-seat, then
    control exits the Cancel boundary to notify-customer

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	engine, err := thresher.New("transaction-sub-process-engine")
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

	// Two orderings are being demonstrated at once: compensation undoes the
	// completed activities in REVERSE (refund before release, mirroring
	// reserve then charge), and only afterwards does control leave through
	// the Cancel boundary to notify-customer. A run that notified first, or
	// undid forwards, would execute every task and complete just the same.
	if err := runLog.check(
		"reserve-seat", "charge-card",
		"refund-card", "release-seat",
		"notify-customer",
	); err != nil {
		return fmt.Errorf("cancel ordering: %w", err)
	}

	fmt.Printf("\n✓ transaction-sub-process completed (%s): the Cancel End "+
		"compensated the booking in reverse order and control left through the "+
		"Cancel boundary\n", state)

	return nil
}
