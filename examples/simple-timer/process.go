package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// buildProcess assembles (timer start ◷) ─> end: the timer holds the token for
// timerDelay, then the single flow reaches the end event.
func buildProcess() (*process.Process, error) {
	// Create process
	proc, err := process.New("simple-timer")
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	// Create timer expression for 3 seconds from now
	timeExpr := goexpr.Must(
		nil,
		data.MustItemDefinition(values.NewVariable(time.Now().Add(timerDelay))),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(time.Now().Add(timerDelay)), nil
		},
		foundation.WithID("timer-3s"),
	)

	// Create timer event definition
	timerDef, err := events.NewTimerEventDefinition(timeExpr, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create timer definition: %w", err)
	}

	// Create timer start event
	timerStart, err := events.NewStartEvent("timer-start",
		events.WithTimerTrigger(timerDef))
	if err != nil {
		return nil, fmt.Errorf("create timer start event: %w", err)
	}

	// Create end event
	endEvent, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end event: %w", err)
	}

	// Add elements to process
	for _, e := range []flow.Element{timerStart, endEvent} {
		if addErr := proc.Add(e); addErr != nil {
			return nil, fmt.Errorf("add element: %w", addErr)
		}
	}

	// Link timer start to end (simple process)
	_, err = flow.Link(timerStart, endEvent)
	if err != nil {
		return nil, fmt.Errorf("link elements: %w", err)
	}

	// Register and start engine
	return proc, nil
}
