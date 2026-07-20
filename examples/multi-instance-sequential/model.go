package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// buildProcess wires start → orders → end, seeding the input `amounts`
// collection the Multi-Instance activity iterates over.
func buildProcess() (*process.Process, error) {
	proc, err := process.New("multi-instance-sequential",
		data.WithProperties(data.MustProperty("amounts",
			data.MustItemDefinition(values.NewArray(100, 250, 80),
				foundation.WithID("amounts")),
			data.ReadyDataState)))
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	orders, err := buildOrdersBody()
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{start, orders, end} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	if _, err := flow.Link(start, orders); err != nil {
		return nil, fmt.Errorf("link start->orders: %w", err)
	}

	if _, err := flow.Link(orders, end); err != nil {
		return nil, fmt.Errorf("link orders->end: %w", err)
	}

	return proc, nil
}

// reportTaxed prints the assembled `taxed` output collection the Multi-Instance
// published once every instance completed (the visibility barrier).
func reportTaxed(ctx context.Context, r service.DataReader) error {
	taxed, err := r.GetData("taxed")
	if err != nil {
		return fmt.Errorf("read taxed collection: %w", err)
	}

	col, ok := taxed.Value().(data.Collection)
	if !ok {
		return fmt.Errorf("taxed is not a collection")
	}

	fmt.Printf("\n  completed — taxed amounts: %v\n", col.GetAll(ctx))

	return nil
}
