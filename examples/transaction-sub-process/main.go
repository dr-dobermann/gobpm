// Command transaction-sub-process demonstrates a Transaction Sub-Process
// (SRD-061, ADR-028): a Sub-Process variant that aborts atomically on a Cancel
// End Event — the ACID-style all-or-nothing unit in BPMN form.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
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

	engine, err := thresher.New("transaction-sub-process-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	// Records the execution order, so the reverse-order claim is checked.
	runLog := newRunLog()

	// The protocol is a datum the engine carries and never reads; the method
	// is left to its default, compensate — the one coordinator the engine has.
	proc, err := buildProcess(runLog,
		activities.WithTransactionProtocol("saga-v1"))
	if err != nil {
		return fmt.Errorf("build process: %w", err)
	}

	// Before running the booking: what registration says to a Transaction
	// whose method names a coordinator this engine does not have.
	if rerr := showRefusal(engine); rerr != nil {
		return rerr
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

	return report(state, runLog)
}
