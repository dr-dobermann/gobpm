package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// buildProcess assembles start → work → end, where work is a Service Task
// marked with a post-tested Standard Loop that runs while loopCounter < 3.
func buildProcess() (*process.Process, error) {
	proc, err := process.New("standard-loop")
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	work, err := loopedWorkTask()
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{start, work, end} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	if _, err := flow.Link(start, work); err != nil {
		return nil, fmt.Errorf("link start->work: %w", err)
	}

	if _, err := flow.Link(work, end); err != nil {
		return nil, fmt.Errorf("link work->end: %w", err)
	}

	return proc, nil
}
