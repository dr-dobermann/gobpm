package main

import (
	"context"
	"fmt"
	"log"
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
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	// Create BPM engine
	engine := thresher.New()

	// Create a process with timer event
	proc, err := process.New("timer-process")
	if err != nil {
		log.Fatal("Failed to create process:", err)
	}

	// Create timer expression for time date (current time + 5 seconds)
	timeExpr := goexpr.Must(
		nil, // no data source needed for static time
		data.MustItemDefinition(values.NewVariable(time.Now().Add(5*time.Second))),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			return values.NewVariable(time.Now().Add(5 * time.Second)), nil
		},
		foundation.WithId("time-plus-5s"),
	)

	// Create timer event definition with time date
	// Note: According to BPMN timer logic, we can use either timeDate OR (timeCycle + timeDuration)
	timerDef, err := events.NewTimerEventDefinition(
		timeExpr, // timeDate - specific time to trigger
		nil,      // timeCycle - not used with timeDate
		nil,      // timeDuration - not used with timeDate
	)
	if err != nil {
		log.Fatal("Failed to create timer event definition:", err)
	}

	// Create start event with timer definition
	timerEvent, err := events.NewStartEvent("timer-start",
		events.WithTimerTrigger(timerDef))
	if err != nil {
		log.Fatal("Failed to create timer start event:", err)
	}

	// Create service operation
	op, err := service.NewOperation("handle-timer", nil, nil, nil)
	if err != nil {
		log.Fatal("Failed to create service operation:", err)
	}

	// Create service task
	serviceTask, err := activities.NewServiceTask("handle-timeout", op,
		activities.WithoutParams())
	if err != nil {
		log.Fatal("Failed to create service task:", err)
	}

	// Create end event
	endEvent, err := events.NewEndEvent("end")
	if err != nil {
		log.Fatal("Failed to create end event:", err)
	}

	// Add elements to process
	for _, element := range []flow.Element{timerEvent, serviceTask, endEvent} {
		if err := proc.Add(element); err != nil {
			log.Fatal("Failed to add element to process:", err)
		}
	}

	// Connect elements with sequence flows
	_, err = flow.Link(timerEvent, serviceTask)
	if err != nil {
		log.Fatal("Failed to link timer event to service task:", err)
	}

	_, err = flow.Link(serviceTask, endEvent)
	if err != nil {
		log.Fatal("Failed to link service task to end event:", err)
	}

	// Register process with engine
	err = engine.RegisterProcess(proc)
	if err != nil {
		log.Fatal("Failed to register process:", err)
	}

	// Start engine
	ctx := context.Background()
	err = engine.Run(ctx)
	if err != nil {
		log.Fatal("Failed to start engine:", err)
	}

	// Start process execution
	err = engine.StartProcess(proc.Id())
	if err != nil {
		log.Fatal("Failed to start process:", err)
	}

	fmt.Printf("Timer process '%s' started successfully with ID: %s\n",
		proc.Name(), proc.Id())
	fmt.Println("Timer will trigger after 5 seconds, repeating 3 times...")
	fmt.Println("Press Ctrl+C to exit")

	// Keep the program running to see timer events
	select {
	case <-ctx.Done():
		fmt.Println("Process completed")
	}
}
