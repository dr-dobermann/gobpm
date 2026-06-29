// Command terminate-end-event demonstrates a Terminate End Event (SRD-030 / BPMN
// §13.5.6): one branch of a parallel process reaches a Terminate End Event and
// abnormally terminates the whole instance, cancelling the other in-flight branch.
//
//	start → split ─┬─ fraud-check ──> terminate-end   (kills the instance)
//	               └─ process-payment ──> payment-done
//
// The process build lives in process.go, the operations in handlers.go; this file is
// the engine wiring + run.
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
  terminate-end-event:
    start → split ─┬─ fraud-check ──> terminate-end   (kills the instance)
                   └─ process-payment ──> payment-done

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	engine, err := thresher.New("terminate-end-event-engine")
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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	h, err := engine.StartProcess(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	fmt.Printf("\n✓ terminate-end-event finished (%s): the fraud branch hit a Terminate "+
		"End Event and ended the whole instance before the payment completed\n", state)

	return nil
}
