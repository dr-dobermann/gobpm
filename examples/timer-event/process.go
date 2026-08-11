package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// buildProcess assembles (timer start ◷) ─> handle-timeout ─> end, the timer
// holding the token for timerDelay before the service task runs.
func buildProcess() (*process.Process, error) {
	proc, err := process.New("timer-process")
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	// Create timer expression for time date (current time + 5 seconds)
	timeExpr := goexpr.Must(
		nil, // no data source needed for static time
		data.MustItemDefinition(values.NewVariable(time.Now().Add(timerDelay))),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(time.Now().Add(timerDelay)), nil
		},
		foundation.WithID("time-plus-5s"),
	)

	// Create timer event definition with time date
	// Note: According to BPMN timer logic, we can use either timeDate OR (timeCycle + timeDuration)
	timerDef, err := events.NewTimerEventDefinition(
		timeExpr, // timeDate - specific time to trigger
		nil,      // timeCycle - not used with timeDate
		nil,      // timeDuration - not used with timeDate
	)
	if err != nil {
		return nil, fmt.Errorf("create timer event definition: %w", err)
	}

	// Create start event with timer definition
	timerEvent, err := events.NewStartEvent("timer-start",
		events.WithTimerTrigger(timerDef))
	if err != nil {
		return nil, fmt.Errorf("create timer start event: %w", err)
	}

	// Create service operation. It needs a real implementation: an operation
	// built with a nil implementation faults the instance the moment the task
	// runs, which is what this example did until its outcome was checked.
	op, err := gooper.New("handle-timer",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Println("  ▶ handle-timeout: the timer fired, handling it")

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("create service operation: %w", err)
	}

	// Create service task
	serviceTask, err := activities.NewServiceTask("handle-timeout", op,
		activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create service task: %w", err)
	}

	// Create end event
	endEvent, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end event: %w", err)
	}

	// Add elements to process
	for _, element := range []flow.Element{timerEvent, serviceTask, endEvent} {
		if addErr := proc.Add(element); addErr != nil {
			return nil, fmt.Errorf("add element: %w", addErr)
		}
	}

	// Connect elements with sequence flows
	for _, l := range [][2]flow.Element{
		{timerEvent, serviceTask}, {serviceTask, endEvent},
	} {
		if linkErr := link(l[0], l[1]); linkErr != nil {
			return nil, linkErr
		}
	}

	return proc, nil
}

// link connects two flow elements with a sequence flow, reporting an element
// that cannot carry one rather than panicking on the assertion.
func link(src, trg flow.Element) error {
	s, ok := src.(flow.SequenceSource)
	if !ok {
		return fmt.Errorf("%q is not a sequence source", src.Name())
	}

	t, ok := trg.(flow.SequenceTarget)
	if !ok {
		return fmt.Errorf("%q is not a sequence target", trg.Name())
	}

	if _, err := flow.Link(s, t); err != nil {
		return fmt.Errorf("link: %w", err)
	}

	return nil
}
