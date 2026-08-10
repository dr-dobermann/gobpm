// Command terminate-end-event demonstrates a Terminate End Event (SRD-030 / BPMN
// §13.5.6): one branch of a parallel process reaches a Terminate End Event and
// abnormally terminates the whole instance, canceling the other in-flight branch.
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

	// Records which tasks ran, so the termination claim is checked.
	ran := newPathSet()

	proc, err := buildProcess(ran)
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

	// Terminate ends the whole instance, so the card must never be charged —
	// that is the outcome this pattern exists to prevent, and checking only
	// that the instance ended would pass even if the charge had gone through
	// on the way out.
	//
	// It deliberately does NOT require that the payment reported an
	// interruption: whether the payment task is scheduled at all before the
	// teardown reaches it is a race, and about one run in eight it is torn
	// down first and never runs. Not running is just as correct as being
	// interrupted, so demanding the interruption made this assertion flaky
	// against perfectly good behavior.
	if err := ran.check([]string{"fraud-check"},
		[]string{"payment-charged"}); err != nil {
		return fmt.Errorf("terminate teardown: %w", err)
	}

	fmt.Printf("\n✓ terminate-end-event finished (%s): the fraud branch hit a Terminate "+
		"End Event and ended the whole instance before the payment completed\n", state)

	return nil
}
