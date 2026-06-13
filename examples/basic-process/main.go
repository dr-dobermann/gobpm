// Command basic-process is the canonical GoBPM quick-start: a minimal
// Start → ServiceTask → End process where the ServiceTask runs YOUR Go code.
//
// The ServiceTask's work is a `gooper` functor — an ordinary Go function
// wrapped as the operation's implementation. This is how you embed arbitrary
// Go logic inside a BPMN process without messages or data mapping: the
// operation carries nil in/out messages and the functor just runs.
//
//	start ─> work (runs a Go functor) ─> end
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	engine, err := thresher.New("basic-process-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	proc, err := process.New("basic-process")
	if err != nil {
		return fmt.Errorf("create process: %w", err)
	}

	// done lets main wait until the ServiceTask's Go code actually ran —
	// engine execution is asynchronous.
	done := make(chan struct{})

	start, err := events.NewStartEvent("start")
	if err != nil {
		return fmt.Errorf("create start: %w", err)
	}

	// The ServiceTask runs a Go functor: an operation with no in/out messages
	// (nil, nil) whose implementation is plain Go code. This is the simplest
	// way to put your logic inside a process.
	work, err := gooper.New(
		func(_ context.Context, _ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Println("  ▶ hello from inside the process (Go code in a ServiceTask)")
			close(done)

			return nil, nil
		})
	if err != nil {
		return fmt.Errorf("create functor: %w", err)
	}

	op, err := service.NewOperation("hello", nil, nil, work)
	if err != nil {
		return fmt.Errorf("create operation: %w", err)
	}

	task, err := activities.NewServiceTask("work", op, activities.WithoutParams())
	if err != nil {
		return fmt.Errorf("create service task: %w", err)
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{start, task, end} {
		if err := proc.Add(e); err != nil {
			return fmt.Errorf("add element: %w", err)
		}
	}

	if _, err := flow.Link(start, task); err != nil {
		return fmt.Errorf("link start->task: %w", err)
	}

	if _, err := flow.Link(task, end); err != nil {
		return fmt.Errorf("link task->end: %w", err)
	}

	if err := engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	if err := engine.StartProcess(proc.ID()); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	// wait for the ServiceTask's functor to run, then a brief grace for the
	// token to reach End.
	select {
	case <-done:
	case <-ctx.Done():
		return fmt.Errorf("timed out waiting for the service task")
	}

	time.Sleep(100 * time.Millisecond)

	fmt.Println("✓ basic-process completed: start → service task (ran Go code) → end")

	return nil
}
