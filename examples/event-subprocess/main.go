// Command event-subprocess demonstrates an interrupting Event Sub-Process
// (ADR-023 v.2 / SRD-052): a scope-armed handler — a SubProcess marked
// triggeredByEvent — that interrupts its enclosing scope when it fires.
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
  event-subprocess:
    start → checkout → await-payment[ awaitPay ⏳ → charge ] → notify → end
                         └─(timer ⚡) payment-timeout → releaseHold

  the payment never arrives, so the timer-triggered handler interrupts the
  wait, runs releaseHold, and the parent resumes on its normal flow.

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	engine, err := thresher.New("event-subprocess-engine",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig())
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	sub := engine.Observe(&scopePrinter{})
	defer sub.Cancel()

	if err = engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	// Records which steps ran, so the interruption claim is checked.
	ran := newPathSet()

	proc, err := buildProcess(ran)
	if err != nil {
		return fmt.Errorf("build process: %w", err)
	}

	if _, err = engine.RegisterProcess(proc); err != nil {
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

	sub.Cancel()

	// The event sub-process is INTERRUPTING: releaseHold must run and charge
	// must not, because the payment never arrives. Checking only that the
	// process completed would pass even if the handler had left the scope's
	// normal flow running and charged an unpaid order.
	if err := ran.check(
		[]string{"checkout", "releaseHold", "notify"},
		[]string{"charge"},
	); err != nil {
		return fmt.Errorf("event sub-process interruption: %w", err)
	}

	fmt.Printf("  ✓ completed (%s)\n", state)

	return nil
}
