package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
)

const (
	orderID   = "ORD-2026-7"
	wantTotal = 100 // len(orderID) * 10
)

// roundTrip is the host's side of the exchange: subscribe to the quote,
// publish the order that instantiates the process, and ASSERT the quote
// carries the total the process computed — a message that arrived with the
// wrong figure, or an output that never reached the end event, would still
// let the process complete.
func roundTrip(ctx context.Context, broker messaging.MessageBroker) error {
	sub, err := broker.Subscribe(ctx, quoteMsgName)
	if err != nil {
		return fmt.Errorf("subscribe to %q: %w", quoteMsgName, err)
	}

	if err = broker.Publish(ctx,
		messaging.Envelope{Name: orderMsgName, Payload: orderID}); err != nil {
		return fmt.Errorf("publish %q: %w", orderMsgName, err)
	}

	fmt.Printf("  → published %q with payload %q\n", orderMsgName, orderID)

	select {
	case env := <-sub.C():
		fmt.Printf("  ← received %q with payload %v\n", env.Name, env.Payload)

		if env.Payload != wantTotal {
			return fmt.Errorf("quote carries %v, want %d", env.Payload, wantTotal)
		}
	case <-ctx.Done():
		return fmt.Errorf("no %q arrived: %w", quoteMsgName, ctx.Err())
	}

	fmt.Println("  ✓ the message route filled the contract and carried its output back")

	return nil
}
