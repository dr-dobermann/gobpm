package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	// Create BPM engine
	engine, err := thresher.New("simple-timer-engine")
	if err != nil {
		log.Fatal("Failed to create BPM engine:", err)
	}

	// Create process
	proc, err := process.New("simple-timer")
	if err != nil {
		log.Fatal("Failed to create process:", err)
	}

	// Create timer expression for 3 seconds from now
	timeExpr := goexpr.Must(
		nil,
		data.MustItemDefinition(values.NewVariable(time.Now().Add(3*time.Second))),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			return values.NewVariable(time.Now().Add(3 * time.Second)), nil
		},
		foundation.WithID("timer-3s"),
	)

	// Create timer event definition
	timerDef, err := events.NewTimerEventDefinition(timeExpr, nil, nil)
	if err != nil {
		log.Fatal("Failed to create timer definition:", err)
	}

	// Create timer start event
	timerStart, err := events.NewStartEvent("timer-start",
		events.WithTimerTrigger(timerDef))
	if err != nil {
		log.Fatal("Failed to create timer start event:", err)
	}

	// Create end event
	endEvent, err := events.NewEndEvent("end")
	if err != nil {
		log.Fatal("Failed to create end event:", err)
	}

	// Add elements to process
	if err := proc.Add(timerStart); err != nil {
		log.Fatal("Failed to add timer start to process:", err)
	}
	if err := proc.Add(endEvent); err != nil {
		log.Fatal("Failed to add end event to process:", err)
	}

	// Link timer start to end (simple process)
	_, err = flow.Link(timerStart, endEvent)
	if err != nil {
		log.Fatal("Failed to link elements:", err)
	}

	// Register and start engine
	if err := engine.RegisterProcess(proc); err != nil {
		log.Fatal("Failed to register process:", err)
	}

	ctx := context.Background()
	if err := engine.Run(ctx); err != nil {
		log.Fatal("Failed to start engine:", err)
	}

	fmt.Printf("Timer process started. Will trigger in 3 seconds...\n")

	// Process will auto-start when timer fires
	// This is just for demo - in real usage you'd handle the timer event
	time.Sleep(5 * time.Second)
	fmt.Println("Timer should have fired by now!")
}
