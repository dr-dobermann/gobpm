package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// step builds an in-process task printing its name; when put is non-empty
// the task also commits put=val — inside a sub-process the commit lands in
// the CHILD scope and dies with it (§10.5.7).
func step(
	saw *sightings, name, put string, val int,
) (*activities.ServiceTask, error) {
	op, err := gooper.New(name,
		func(_ context.Context, ds service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			if d, err := ds.GetData("order-id"); err == nil {
				id := d.Value().Get(context.Background())
				saw.record(name, id)

				fmt.Printf("  %s (sees order-id=%v)\n", name, id)
			} else {
				// Recorded as nil rather than skipped: a failed walk-up is
				// exactly what the assertion in main needs to catch, and
				// printing a shorter line used to hide it.
				saw.record(name, nil)

				fmt.Printf("  %s (could not see order-id)\n", name)
			}

			if put == "" {
				return nil, nil
			}

			return data.MustItemDefinition(
				intValue(val), foundation.WithID(put)), nil
		})
	if err != nil {
		return nil, fmt.Errorf("create %s operation: %w", name, err)
	}

	return activities.NewServiceTask(name, op, activities.WithoutParams())
}
