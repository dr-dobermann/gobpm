package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// fraudCheckOp is a quick check that flags the order as fraudulent. The branch it
// runs on leads straight to a Terminate End Event, so the whole instance is about to
// be torn down.
func fraudCheckOp(ran *pathSet) (service.Operation, error) {
	return gooper.New("fraud-check",
		func(_ context.Context, _ service.DataReader, _ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			ran.mark("fraud-check")
			fmt.Println("  ⚠ fraud-check: fraudulent order detected — terminating the process")

			return nil, nil
		})
}

// paymentOp is a long-running, context-honoring charge. When the fraud branch
// terminates the instance, this operation's context is cancelled and it abandons the
// charge instead of completing.
func paymentOp(ran *pathSet) (service.Operation, error) {
	return gooper.New("process-payment",
		func(ctx context.Context, _ service.DataReader, _ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			fmt.Println("  → process-payment: charging the card (takes ~3s)...")

			select {
			case <-time.After(3 * time.Second):
				ran.mark("payment-charged")
				fmt.Println("  ✓ process-payment: charged")

				return nil, nil

			case <-ctx.Done():
				ran.mark("payment-interrupted")
				fmt.Println("  ✗ process-payment: interrupted before it finished")

				return nil, ctx.Err()
			}
		})
}
