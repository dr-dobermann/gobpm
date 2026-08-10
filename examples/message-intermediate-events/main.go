// Command message-intermediate-events demonstrates BPMN message events
// (SRD-014 / ADR-014 v.1): an IntermediateThrowEvent publishes a message to the
// engine's MessageBroker, and downstream an IntermediateCatchEvent waits for it
// (through a MessageWaiter) and binds the payload into scope — the event-shaped
// peers of the SendTask/ReceiveTask.
//
//	start ─> throw-order ─> catch-order ─> confirm ─> end
//	         (publishes      (waits on the   (reads the
//	          "order          broker, binds   bound payload)
//	          placed")        the payload)
//
// The events live on one track, so the throw completes before the catch
// subscribes; the in-memory broker buffers the published message until then.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

const orderID = "ORD-2026-002"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print(`
  message-intermediate-events:
    start ─> throw-order ─> catch-order ─> confirm ─> end
             (publishes      (waits on the   (reads the
              "order          broker, binds   bound payload)
              placed")        the payload)

`)
	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("create default states: %w", err)
	}

	engine, err := thresher.New("message-events-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	done := make(chan string, 1)

	proc, err := buildProcess(done)
	if err != nil {
		return err
	}

	if _, err := engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	if _, err := engine.StartLatest(proc.ID()); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	var got string
	select {
	case got = <-done:
	case <-ctx.Done():
		return fmt.Errorf("timed out waiting for the message-event round-trip")
	}

	if got != orderID {
		return fmt.Errorf("catch-order: want %q, got %q", orderID, got)
	}

	fmt.Printf("  ✓ throw-order published %q\n", orderID)
	fmt.Printf("  ✓ catch-order bound it; confirm read order_in = %q\n", got)
	fmt.Println("✓ message-events-demo completed: the message traveled the " +
		"broker from the throw event to the catch event")

	return nil
}

// link wires src -> trg with a sequence flow.
func link(src, trg flow.Element) error {
	s, ok := src.(flow.SequenceSource)
	if !ok {
		return fmt.Errorf("%q is not a sequence source", src.Name())
	}

	t, ok := trg.(flow.SequenceTarget)
	if !ok {
		return fmt.Errorf("%q is not a sequence target", trg.Name())
	}

	if _, err := flow.Link(s, t); err != nil {
		return fmt.Errorf("link: %w", err)
	}

	return nil
}
