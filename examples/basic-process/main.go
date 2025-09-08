package main

import (
	"context"
	"fmt"
	"log"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	// Create BPM engine
	engine, err := thresher.New("basic-process-engine")
	if err != nil {
		log.Fatal("Failed to create BPM engine:", err)
	}

	// Create a simple process
	proc, err := process.New("simple-process")
	if err != nil {
		log.Fatal("Failed to create process:", err)
	}

	// Create start event
	startEvent, err := events.NewStartEvent("start")
	if err != nil {
		log.Fatal("Failed to create start event:", err)
	}

	// Create service operation (simplified)
	op, err := service.NewOperation("hello-world", nil, nil, nil)
	if err != nil {
		log.Fatal("Failed to create service operation:", err)
	}

	// Create service task
	serviceTask, err := activities.NewServiceTask("process-data", op,
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
	if err := proc.Add(startEvent); err != nil {
		log.Fatal("Failed to add start event to process:", err)
	}
	if err := proc.Add(serviceTask); err != nil {
		log.Fatal("Failed to add service task to process:", err)
	}
	if err := proc.Add(endEvent); err != nil {
		log.Fatal("Failed to add end event to process:", err)
	}

	// Connect elements with sequence flows
	_, err = flow.Link(startEvent, serviceTask)
	if err != nil {
		log.Fatal("Failed to link start event to service task:", err)
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

	fmt.Printf("Process '%s' started successfully with ID: %s\n",
		proc.Name(), proc.Id())
}
