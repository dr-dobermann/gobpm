package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// ratesTask builds an in-process task whose operation commits a data-keyed
// rates map under the item id "rates" — committing it at the activity boundary
// is what the commit-diff walks per entry (ADR-011 v.7 §2.9.4/§2.9.7).
func ratesTask(name string, rates map[string]float64,
) (*activities.ServiceTask, error) {
	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Printf("  %s → commit rates=%v\n", name, rates)

			return data.MustItemDefinition(
				values.MustMap(rates),
				foundation.WithID("rates")), nil
		})
	if err != nil {
		return nil, fmt.Errorf("create %s operation: %w", name, err)
	}

	task, err := activities.NewServiceTask(name, op, activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create %s task: %w", name, err)
	}

	return task, nil
}

// buildProcess assembles start → publish (rates={EUR,GBP}) → reprice
// (rates={EUR',JPY}) → end. The first commit is ONE Value_Added at the rates
// root; the re-commit is per-entry — rates["EUR"] updated, rates["JPY"] added,
// rates["GBP"] deleted — each surfaced as a DataChange fact with a ["key"] path.
func buildProcess() (*process.Process, error) {
	proc, err := process.New("maps")
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	publish, err := ratesTask("publish",
		map[string]float64{"EUR": 1.08, "GBP": 1.27})
	if err != nil {
		return nil, err
	}

	reprice, err := ratesTask("reprice",
		map[string]float64{"EUR": 1.09, "JPY": 161})
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{start, publish, reprice, end} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	links := []func() error{
		func() error { _, e := flow.Link(start, publish); return e },
		func() error { _, e := flow.Link(publish, reprice); return e },
		func() error { _, e := flow.Link(reprice, end); return e },
	}
	for _, link := range links {
		if err := link(); err != nil {
			return nil, fmt.Errorf("link flow: %w", err)
		}
	}

	return proc, nil
}
