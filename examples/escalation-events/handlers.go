package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// notifyManager builds the ServiceTask on the boundary's exception flow — the
// escalation handler that runs when the order review escalates over budget.
func notifyManager() (*activities.ServiceTask, error) {
	op, err := gooper.New("notify-manager",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Println("  → notify-manager: order escalated (OVER_BUDGET), " +
				"routing to a human approver")

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("notify-manager op: %w", err)
	}

	st, err := activities.NewServiceTask("notify-manager", op,
		activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("notify-manager task: %w", err)
	}

	return st, nil
}
