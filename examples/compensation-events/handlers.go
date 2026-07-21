package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// bookTask builds a booking step that prints its action.
func bookTask(name, msg string) (*activities.ServiceTask, error) {
	op, err := gooper.New(name+"-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Println(msg)

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("%s op: %w", name, err)
	}

	st, err := activities.NewServiceTask(name, op, activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("%s task: %w", name, err)
	}

	return st, nil
}

// undoTask builds an isForCompensation handler that prints its undo action —
// it lives outside the normal flow and runs only when compensation is thrown.
func undoTask(name, msg string) (*activities.ServiceTask, error) {
	op, err := gooper.New(name+"-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Println(msg)

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("%s op: %w", name, err)
	}

	st, err := activities.NewServiceTask(name, op,
		activities.WithoutParams(), activities.WithCompensation())
	if err != nil {
		return nil, fmt.Errorf("%s task: %w", name, err)
	}

	return st, nil
}

// compBoundary attaches a Compensation boundary on host routing to handler.
func compBoundary(
	name string, host flow.ActivityNode, handler flow.ActivityNode,
) (*events.BoundaryEvent, error) {
	ced, err := events.NewCompensationEventDefinition(nil, true)
	if err != nil {
		return nil, fmt.Errorf("%s definition: %w", name, err)
	}

	be, err := events.NewCompensationBoundaryEvent(name, host, ced, handler)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	return be, nil
}
