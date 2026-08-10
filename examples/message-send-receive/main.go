// Command message-send-receive demonstrates broker-backed message handling
// (SRD-013 / ADR-014 v.1): a SendTask binds a process property and publishes it
// to the engine's MessageBroker; downstream a ReceiveTask waits for that message
// through a MessageWaiter, and on arrival binds the payload into scope, where an
// output association lands it in a DataObject.
//
//	start ─> send-order ─> receive-order ─> confirm ─> end
//	         (publishes      (waits on the      (signals
//	          "order         broker, binds       completion)
//	          placed")        the payload)
//
// The tasks live on one track, so the send completes before the receive
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

const orderID = "ORD-2026-001"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print(`
  message-send-receive:
    start ─> send-order ─> receive-order ─> confirm ─> end
             (publishes      (waits on the      (signals
              "order         broker, binds       completion)
              placed")        the payload)

`)
	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("create default states: %w", err)
	}

	engine, err := thresher.New("message-demo-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	done := make(chan struct{})

	var received string

	proc, err := buildProcess(done, &received)
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

	select {
	case <-done:
	case <-ctx.Done():
		return fmt.Errorf("timed out waiting for the message round-trip")
	}

	if received != orderID {
		return fmt.Errorf("received-order: want %q, got %q", orderID, received)
	}

	fmt.Printf("  ✓ send-order published %q\n", orderID)
	fmt.Printf("  ✓ receive-order bound it into received-order = %q\n", received)
	fmt.Println("✓ message-demo completed: the message traveled the broker " +
		"from the SendTask to the ReceiveTask")

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
