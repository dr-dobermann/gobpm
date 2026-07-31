package main

import (
	"context"
	"fmt"
	"time"

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

// buildProcess assembles start → work(greet) → end, with a process-level
// userName is the property value the ServiceTask reads back — declared once
// and read by both the process data and the check, so they cannot drift.
const userName = "dr.Dobermann"

// "user_name" property the ServiceTask reads at runtime.
func buildProcess() (*process.Process, error) {
	proc, err := process.New("basic-process",
		data.WithProperties(
			data.MustProperty("user_name",
				data.MustItemDefinition(
					values.NewVariable(userName),
					foundation.WithID("user_name")),
				data.ReadyDataState)))
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	op, err := greetOp()
	if err != nil {
		return nil, fmt.Errorf("create operation: %w", err)
	}

	task, err := activities.NewServiceTask("work", op, activities.WithoutParams())
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

// greetOp is the ServiceTask's Go functor. It reads the process "user_name"
// property AND the engine's RUNTIME/STARTED_AT variable through its read-only
// DataReader, then greets the user — showing how a gofunc reaches process data
// and runtime variables without any message ceremony.
func greetOp() (service.Operation, error) {
	return gooper.New("greet",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			user, err := r.GetData("user_name")
			if err != nil {
				return nil, fmt.Errorf("read user_name: %w", err)
			}

			started, err := r.GetData("RUNTIME/STARTED_AT")
			if err != nil {
				return nil, fmt.Errorf("read RUNTIME/STARTED_AT: %w", err)
			}

			// Both reads are the demonstration — a declared property and an
			// engine-published RUNTIME variable — so both are checked. A
			// property that resolved to nothing, or a RUNTIME var that came
			// back zero, would print a odd-looking line and complete.
			if got := user.Value().Get(ctx); got != userName {
				return nil, fmt.Errorf("user_name = %v, want %q",
					got, userName)
			}

			at, ok := started.Value().Get(ctx).(time.Time)
			if !ok || at.IsZero() {
				return nil, fmt.Errorf(
					"RUNTIME/STARTED_AT = %v, want a real start time",
					started.Value().Get(ctx))
			}

			fmt.Printf("  ▶ hello, %v (instance started at %v)\n",
				user.Value().Get(ctx), at)

			return nil, nil
		})
}
