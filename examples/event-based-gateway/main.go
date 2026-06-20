// Command event-based-gateway demonstrates the Event-Based gateway (ADR-005 §2.12)
// as a deferred choice: the gate waits for an approval message OR a 10s timeout timer
// and routes the token down whichever fires first, dropping the other subscription.
// The demo publishes the approval message, so the approval arm wins; the timer is the
// self-terminating fallback (the run completes even if no message arrives).
//
//	start → event-gate ─┬→ catch(approval message) → approved → end
//	                    └→ catch(10s timeout timer) → timedOut → end
//
// The process build lives in process.go; this file is the engine wiring + run.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	broker := membroker.New()

	engine, err := thresher.New("event-based-gateway-engine",
		thresher.WithMessageBroker(broker))
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	proc, err := buildProcess()
	if err != nil {
		return fmt.Errorf("build process: %w", err)
	}

	if err := engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	fmt.Println("deferred choice: waiting for an approval message OR a 10s timeout...")

	h, err := engine.StartProcess(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	time.Sleep(300 * time.Millisecond) // let the gate park on both arms

	if err := broker.Publish(ctx, messaging.Envelope{
		Name: approvalMessage, Payload: "OK"}); err != nil {
		return fmt.Errorf("publish approval: %w", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	fmt.Printf("✓ event-based-gateway completed (%s): the gate fired the arm "+
		"whose event arrived first; the other was dropped\n", state)

	return nil
}
