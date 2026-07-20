package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// buildProcess wires start → panel → end, seeding the `reviewers` collection the
// parallel Multi-Instance activity fans out over.
func buildProcess() (*process.Process, error) {
	proc, err := process.New("multi-instance-parallel",
		data.WithProperties(data.MustProperty("reviewers",
			data.MustItemDefinition(
				values.NewArray("Ann", "Bob", "Cara", "Dan"),
				foundation.WithID("reviewers")),
			data.ReadyDataState)))
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	panel, err := buildPanel()
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{start, panel, end} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	if _, err := flow.Link(start, panel); err != nil {
		return nil, fmt.Errorf("link start->panel: %w", err)
	}

	if _, err := flow.Link(panel, end); err != nil {
		return nil, fmt.Errorf("link panel->end: %w", err)
	}

	return proc, nil
}
