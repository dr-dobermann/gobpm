package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// voteTask reads the per-instance `reviewer` name and prints the vote — the work
// each Multi-Instance body instance performs before it completes.
func voteTask() (*activities.ServiceTask, error) {
	op, err := gooper.New("vote",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			rv, err := r.GetData("reviewer")
			if err != nil {
				return nil, fmt.Errorf("read reviewer: %w", err)
			}

			name, _ := rv.Value().Get(ctx).(string)
			fmt.Printf("    %s votes\n", name)

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("create vote op: %w", err)
	}

	task, err := activities.NewServiceTask("vote", op, activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create vote task: %w", err)
	}

	return task, nil
}

// buildNotification builds the non-interrupting boundary on the board that catches
// the quorum-reached signal (catchDef), plus its notification side-flow: boundary →
// notify → notify-end. Non-interrupting, so the board keeps running.
func buildNotification(
	board *activities.SubProcess, catchDef flow.EventDefinition,
) (*events.BoundaryEvent, *activities.ServiceTask, *events.EndEvent, error) {
	boundary, err := events.NewBoundaryEvent("quorum-bnd", board, catchDef, false)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create boundary: %w", err)
	}

	op, err := gooper.New("notify",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Println("    → quorum reached — notifying the chair")

			return nil, nil
		})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create notify op: %w", err)
	}

	notify, err := activities.NewServiceTask("notify", op,
		activities.WithoutParams())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create notify task: %w", err)
	}

	notifyEnd, err := events.NewEndEvent("notify-end")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create notify-end: %w", err)
	}

	return boundary, notify, notifyEnd, nil
}
