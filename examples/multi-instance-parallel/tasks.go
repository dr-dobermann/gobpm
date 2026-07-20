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

// scoreTask reads the per-instance `reviewer` name and its 0-based loopCounter,
// prints the reviewer's score, and returns `score` — the output item the panel
// assembles into the `scores` collection.
func scoreTask() (*activities.ServiceTask, error) {
	op, err := gooper.New("score",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			lc, err := r.GetData("loopCounter")
			if err != nil {
				return nil, fmt.Errorf("read loopCounter: %w", err)
			}

			rv, err := r.GetData("reviewer")
			if err != nil {
				return nil, fmt.Errorf("read reviewer: %w", err)
			}

			i, _ := lc.Value().Get(ctx).(int)
			name, _ := rv.Value().Get(ctx).(string)
			score := 70 + i*5

			fmt.Printf("    %s scores the proposal: %d\n", name, score)

			return data.MustItemDefinition(
				values.NewVariable(score), foundation.WithID("score")), nil
		})
	if err != nil {
		return nil, fmt.Errorf("create score op: %w", err)
	}

	task, err := activities.NewServiceTask("score", op,
		activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create score task: %w", err)
	}

	return task, nil
}
