package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// loopedWorkTask builds a Service Task marked with a post-tested Standard Loop
// wantPasses is how many times the post-tested loop runs — read by the loop
// condition and by the example's own assertion, so the check cannot drift away
// from the behaviour it checks.
const wantPasses = 3

// (loopCounter < 3): each pass reads the engine-published loopCounter and prints
// it, so the three iterations are visible.
func loopedWorkTask(log *runLog) (*activities.ServiceTask, error) {
	op, err := gooper.New("work",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := r.GetData("loopCounter")
			if err != nil {
				return nil, err
			}

			log.mark(fmt.Sprint(d.Value().Get(ctx)))
			fmt.Printf("    iteration: loopCounter=%v\n", d.Value().Get(ctx))

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("create work op: %w", err)
	}

	loop, err := activities.NewStandardLoop(loopCounterBelow(wantPasses))
	if err != nil {
		return nil, fmt.Errorf("create standard loop: %w", err)
	}

	work, err := activities.NewServiceTask("work", op,
		activities.WithLoop(loop), activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create work task: %w", err)
	}

	return work, nil
}

// loopCounterBelow builds the boolean loop condition "loopCounter < n", read
// each pass through the engine-published counter.
func loopCounterBelow(n int) data.FormalExpression {
	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "loopCounter")
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(int)

			return values.NewVariable(v < n), nil
		})
}
