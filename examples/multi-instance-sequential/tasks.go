package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// taxTask reads the per-instance `amount`, applies a 20% tax, prints the step,
// and returns `withTax` — the output item the Multi-Instance assembles.
func taxTask() (*activities.ServiceTask, error) {
	op, err := gooper.New("tax",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := r.GetData("amount")
			if err != nil {
				return nil, fmt.Errorf("read amount: %w", err)
			}

			amount, _ := d.Value().Get(ctx).(int)
			withTax := amount + amount/5 // +20%

			fmt.Printf("    order: amount=%d → withTax=%d\n", amount, withTax)

			return data.MustItemDefinition(
				values.NewVariable(withTax), foundation.WithID("withTax")), nil
		})
	if err != nil {
		return nil, fmt.Errorf("create tax op: %w", err)
	}

	task, err := activities.NewServiceTask("tax", op,
		activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create tax task: %w", err)
	}

	return task, nil
}
