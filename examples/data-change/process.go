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

// receiptTask builds an in-process task whose operation returns receipt={sum}
// — committing it at the task's activity boundary is exactly what the
// commit-diff observes (ADR-011 v.6 §2.9.4).
func receiptTask(name string, sum int) (*activities.ServiceTask, error) {
	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Printf("  %s → commit receipt={sum:%d}\n", name, sum)

			return data.MustItemDefinition(
				values.MustRecord(values.F("sum", values.NewVariable(sum))),
				foundation.WithID("receipt")), nil
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

// buildProcess assembles start → produce (receipt={sum:5}) → reprice
// (receipt={sum:6}) → end. The first commit surfaces as ONE Value_Added at
// the receipt root; the re-commit as ONE Value_Updated at the changed leaf.
func buildProcess() (*process.Process, error) {
	proc, err := process.New("data-change")
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	produce, err := receiptTask("produce", 5)
	if err != nil {
		return nil, err
	}

	reprice, err := receiptTask("reprice", 6)
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{start, produce, reprice, end} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	links := []func() error{
		func() error { _, e := flow.Link(start, produce); return e },
		func() error { _, e := flow.Link(produce, reprice); return e },
		func() error { _, e := flow.Link(reprice, end); return e },
	}
	for _, link := range links {
		if err := link(); err != nil {
			return nil, fmt.Errorf("link flow: %w", err)
		}
	}

	return proc, nil
}
