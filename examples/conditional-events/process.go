package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// commitTask builds an in-process task committing name=value at its activity
// boundary — the committed change is what wakes a conditional subscription
// (ADR-006 v.3 §2.7).
func commitTask(taskName, dataName string, value int) (*activities.ServiceTask, error) {
	op, err := gooper.New(taskName,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Printf("  %s → commit %s=%d\n", taskName, dataName, value)

			return data.MustItemDefinition(
				values.NewVariable(value),
				foundation.WithID(dataName)), nil
		})
	if err != nil {
		return nil, fmt.Errorf("create %s operation: %w", taskName, err)
	}

	return activities.NewServiceTask(taskName, op, activities.WithoutParams())
}

// notifyTask prints the released-path marker.
func notifyTask() (*activities.ServiceTask, error) {
	op, err := gooper.New("notify",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Println("  notify → the total crossed 100, shipping upgraded")

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("create notify operation: %w", err)
	}

	return activities.NewServiceTask("notify", op, activities.WithoutParams())
}

// totalAbove builds the watch condition "total > n". WithDependencies("total")
// narrows re-evaluation to commits touching "total" — without it the engine
// safely re-evaluates on every non-empty commit.
func totalAbove(n int) (data.FormalExpression, error) {
	return goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "total")
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(int)

			return values.NewVariable(v > n), nil
		},
		goexpr.WithDependencies("total"))
}
