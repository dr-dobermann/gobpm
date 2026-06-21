package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

const orderID = "ORD-7"

// fulfill publishes the two correlated messages and waits for the event-born
// instance (no StartProcess — the gate instantiates) to complete on both.
func fulfill(
	ctx context.Context, engine *thresher.Thresher, broker *membroker.Broker,
) error {
	fmt.Println("publishing 'order placed' (creates the instance)...")

	if err := broker.Publish(ctx, messaging.Envelope{
		Name: orderMessage, Payload: orderID, CorrelationKey: orderID,
	}); err != nil {
		return fmt.Errorf("publish order: %w", err)
	}

	h, err := awaitInstance(ctx, engine)
	if err != nil {
		return err
	}

	fmt.Println("publishing 'payment received' (routes to the same instance)...")

	if err := broker.Publish(ctx, messaging.Envelope{
		Name: paymentMessage, Payload: orderID, CorrelationKey: orderID,
	}); err != nil {
		return fmt.Errorf("publish payment: %w", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	fmt.Printf("✓ order-fulfillment completed (%s): one instance, born by the "+
		"first message, finished once both arms fired\n", state)

	return nil
}

// awaitInstance polls until the gate has instantiated an instance and returns its
// observation handle (the SRD-019 discovery API over an event-born instance).
func awaitInstance(
	ctx context.Context, engine *thresher.Thresher,
) (*thresher.InstanceHandle, error) {
	for {
		if ids := engine.Instances(thresher.InstancesAll); len(ids) > 0 {
			if h, ok := engine.Instance(ids[0]); ok {
				return h, nil
			}
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("no instance created: %w", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}
