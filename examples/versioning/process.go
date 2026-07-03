package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// processKey is the shared versioning key — the process id (ADR-019 §2.1: the
// key is the BPMN id, not the display name). Every build below carries this
// same id, so successive registrations become versions v1, v2, … of ONE
// definition rather than separate unrelated processes.
const processKey = "greeter"

// buildGreeter builds a trivial start → service task → end process whose task
// prints the given release label, so the console shows WHICH version ran when a
// version is later addressed by latest / number / handle.
func buildGreeter(label string) (*process.Process, error) {
	proc, err := process.New("greeter", foundation.WithID(processKey))
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	op, err := greetOp(label)
	if err != nil {
		return nil, fmt.Errorf("create operation: %w", err)
	}

	task, err := activities.NewServiceTask("greet", op, activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create service task: %w", err)
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{start, task, end} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	if _, err := flow.Link(start, task); err != nil {
		return nil, fmt.Errorf("link start->task: %w", err)
	}

	if _, err := flow.Link(task, end); err != nil {
		return nil, fmt.Errorf("link task->end: %w", err)
	}

	return proc, nil
}

// greetOp returns a Go operation that prints the release label of the running
// version.
func greetOp(label string) (service.Operation, error) {
	return gooper.New("greet",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Printf("      ▶ [%s] hello from the greeter\n", label)

			return nil, nil
		})
}
