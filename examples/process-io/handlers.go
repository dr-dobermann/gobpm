package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// taxTask is the child's only work: read the bound "amount", write "total"
// = amount + 20%. The returned item lands in the child's root scope under
// its id, which is where the contract reads "total" at completion.
func taxTask() (*activities.ServiceTask, error) {
	op, err := gooper.New("tax",
		func(ctx context.Context, ds service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := ds.GetData("amount")
			if err != nil {
				return nil, err
			}

			amount, _ := d.Value().Get(ctx).(int)
			fmt.Printf("    (rate) amount=%d → total=%d\n", amount,
				amount+amount/5)

			return data.MustItemDefinition(values.NewVariable(amount+amount/5),
				foundation.WithID("total")), nil
		})
	if err != nil {
		return nil, err
	}

	return activities.NewServiceTask("tax", op, activities.WithoutParams())
}

// stampTask publishes an engine runtime variable through the contract: a
// RUNTIME/… variable never leaves an instance by itself, so the task reads
// STARTED_AT by its path and writes it under the declared "started_at"
// output (ADR-040 §2.3a). It also reads the child's "total" back, so the
// example can assert the round trip.
func stampTask(got *seen) (*activities.ServiceTask, error) {
	op, err := gooper.New("stamp",
		func(ctx context.Context, ds service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			total, err := ds.GetData("total")
			if err != nil {
				return nil, err
			}

			got.record(total.Value().Get(ctx))
			fmt.Printf("  ✓ quote sees total=%v\n", total.Value().Get(ctx))

			started, err := ds.GetData("RUNTIME/STARTED_AT")
			if err != nil {
				return nil, fmt.Errorf("read RUNTIME/STARTED_AT: %w", err)
			}

			at, _ := started.Value().Get(ctx).(time.Time)

			return data.MustItemDefinition(values.NewVariable(at),
				foundation.WithID("started_at")), nil
		})
	if err != nil {
		return nil, err
	}

	return activities.NewServiceTask("stamp", op, activities.WithoutParams())
}
