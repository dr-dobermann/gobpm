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

// buildProcess assembles: start → triage (Ad-Hoc) → end.
//
// The triage container holds four activities and NO sequence flows between
// them — an Ad-Hoc Sub-Process is exactly that: work whose order the model
// declines to fix. What runs, and when, is the Router's answer.
func buildProcess(log *runLog, severity string) (*process.Process, error) {
	proc, err := process.New("incident-triage",
		data.WithProperties(
			data.MustProperty("severity",
				data.MustItemDefinition(
					values.NewVariable(severity),
					foundation.WithID("severity")),
				data.ReadyDataState)))
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	// WithAdHoc attaches the Router. Nothing is implied: without it the
	// container would be rejected at registration rather than run in some
	// arbitrary order.
	triage, err := activities.NewSubProcess("triage",
		activities.WithAdHoc(triageRouter{}))
	if err != nil {
		return nil, fmt.Errorf("create ad-hoc sub-process: %w", err)
	}

	for _, step := range []struct{ name, says string }{
		{"gather-logs", "collected the incident logs"},
		{"notify-customer", "told the customer we are on it"},
		{"escalate", "paged the on-call engineer"},
		{"close-incident", "closed the incident"},
	} {
		task, terr := reportTask(log, step.name, step.says)
		if terr != nil {
			return nil, terr
		}

		if terr := triage.Add(task); terr != nil {
			return nil, fmt.Errorf("add %s: %w", step.name, terr)
		}
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{start, triage, end} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	if _, err := flow.Link(start, triage); err != nil {
		return nil, fmt.Errorf("link start->triage: %w", err)
	}

	if _, err := flow.Link(triage, end); err != nil {
		return nil, fmt.Errorf("link triage->end: %w", err)
	}

	return proc, nil
}

// reportTask builds a Service Task that announces what it did. Inside an ad-hoc
// container the activities are ordinary tasks — only their succession differs.
func reportTask(
	log *runLog, name, says string,
) (*activities.ServiceTask, error) {
	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			log.mark(name)
			fmt.Printf("    ▶ %s: %s\n", name, says)

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("create %s operation: %w", name, err)
	}

	// The Router's state is keyed by activity ID, so an ad-hoc activity is worth
	// giving an explicit, readable one: the routing code then reads like the
	// diagram instead of juggling generated numbers.
	task, err := activities.NewServiceTask(name, op,
		activities.WithoutParams(), foundation.WithID(name))
	if err != nil {
		return nil, fmt.Errorf("create %s task: %w", name, err)
	}

	return task, nil
}
