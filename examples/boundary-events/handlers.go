package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// paymentTask builds the long-running guarded activity: a ServiceTask that would
// take ~4s but honours its context, so when the timer boundary fires and the engine
// cancels the track, it returns early. Its result is then discarded by the
// interruption checkpoint (SRD-029 §3.7) — the normal "paid" flow is never taken.
func paymentTask() (*activities.ServiceTask, error) {
	op, err := gooper.New("process-payment",
		func(ctx context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			fmt.Println("  → process-payment: charging the card (takes ~4s)...")

			select {
			case <-time.After(4 * time.Second):
				fmt.Println("  → process-payment: charged")

				return nil, nil

			case <-ctx.Done():
				fmt.Println("  ✗ process-payment: interrupted before it finished")

				return nil, ctx.Err()
			}
		})
	if err != nil {
		return nil, fmt.Errorf("payment op: %w", err)
	}

	task, err := activities.NewServiceTask(
		"process-payment", op, activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("payment task: %w", err)
	}

	return task, nil
}

// printTask builds a ServiceTask that prints msg when it runs — used for the
// exception handler the boundary routes to.
func printTask(id, msg string) (*activities.ServiceTask, error) {
	op, err := gooper.New(id,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			fmt.Println(msg)

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("op %q: %w", id, err)
	}

	task, err := activities.NewServiceTask(id, op, activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("task %q: %w", id, err)
	}

	return task, nil
}
