package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// signalName is the broadcast signal both processes start on.
const signalName = "order-received"

// signalDef builds a name-based signal event definition (signals match by name,
// carry no correlation).
func signalDef(name string) (*events.SignalEventDefinition, error) {
	sig, err := events.NewSignal(name, nil)
	if err != nil {
		return nil, fmt.Errorf("create signal: %w", err)
	}

	return events.NewSignalEventDefinition(sig)
}

// signalStartProcess builds a process whose START is a signal StartEvent (no
// incoming flow): start(signal) → handler(ServiceTask) → end. A broadcast of
// signalName instantiates it — there is no StartProcess call.
func signalStartProcess(id, handling string) (*process.Process, error) {
	def, err := signalDef(signalName)
	if err != nil {
		return nil, err
	}

	start, err := events.NewStartEvent("start", events.WithSignalTrigger(def))
	if err != nil {
		return nil, fmt.Errorf("create signal start: %w", err)
	}

	handler, err := printTask(id+"-handle", handling)
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	proc, err := process.New(id)
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	for _, e := range []flow.Element{start, handler, end} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	if _, err := flow.Link(start, handler); err != nil {
		return nil, fmt.Errorf("link start->handler: %w", err)
	}

	if _, err := flow.Link(handler, end); err != nil {
		return nil, fmt.Errorf("link handler->end: %w", err)
	}

	return proc, nil
}

// printTask is a ServiceTask whose Go operation prints msg when the task runs.
func printTask(id, msg string) (*activities.ServiceTask, error) {
	op, err := gooper.New(id,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Println(msg)

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("op %q: %w", id, err)
	}

	task, err := activities.NewServiceTask(id, op, activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("task %q: %w", id, err)
	}

	return task, nil
}
