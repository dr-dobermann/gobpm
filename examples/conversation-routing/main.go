// Command conversation-routing demonstrates SRD-017 phase-2c: conversation-token
// threading — a follow-up message routes back to the specific running instance
// it belongs to, by correlation key.
//
//	external (this program)            order-handler (auto-instantiated per order)
//	  publish "order placed" ORD-1 ─▶  born(ORD-1) ─▶ await "payment received"
//	  publish "order placed" ORD-2 ─▶  born(ORD-2) ─▶ await "payment received"
//	  publish "payment received" ORD-1 ───────────▶  routed to the ORD-1 handler
//	  publish "payment received" ORD-2 ───────────▶  routed to the ORD-2 handler
//
// Each order spawns a keyed handler instance (phase-2b instantiation) that then
// waits for its payment. Because the in-instance receiver subscribes keyed to
// its conversation, each "payment received" routes back to the originating
// handler — never the other one. The program verifies each handler reported its
// own order paired with its own payment (no cross-talk) and exits 0.
package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// orders are the distinct conversations driven concurrently.
var orders = []string{"ORD-1", "ORD-2"}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("create default states: %w", err)
	}

	broker := membroker.New()

	engine, err := thresher.New("conversation-routing-demo",
		thresher.WithMessageBroker(broker))
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	done := make(chan string, len(orders))

	handler, err := newHandler(done)
	if err != nil {
		return err
	}

	if err := engine.RegisterProcess(handler); err != nil {
		return fmt.Errorf("register handler: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	// 1. each order spawns a keyed handler instance.
	for _, o := range orders {
		if err := broker.Publish(ctx, messaging.Envelope{
			Name: orderMsg, Payload: o, CorrelationKey: o}); err != nil {
			return fmt.Errorf("publish order %s: %w", o, err)
		}
	}

	// let both handlers reach and park their payment receiver.
	time.Sleep(300 * time.Millisecond)

	// 2. each payment routes back to its own order's handler.
	for _, o := range orders {
		if err := broker.Publish(ctx, messaging.Envelope{
			Name: paymentMsg, Payload: o, CorrelationKey: o}); err != nil {
			return fmt.Errorf("publish payment %s: %w", o, err)
		}
	}

	// 3. verify: each handler reported its own order paired with its own payment.
	for range orders {
		select {
		case r := <-done:
			fmt.Printf("handler reported order/payment: %s\n", r)

			order, pay, _ := strings.Cut(r, "/")
			if order != pay {
				return fmt.Errorf(
					"cross-talk: order %q received payment %q", order, pay)
			}

		case <-time.After(5 * time.Second):
			return fmt.Errorf("a payment did not route back to its handler")
		}
	}

	fmt.Println("OK: each payment routed to its originating handler conversation")

	return nil
}
