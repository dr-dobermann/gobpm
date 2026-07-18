package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// step builds a ServiceTask that announces itself when it runs.
func step(name string) (*activities.ServiceTask, error) {
	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Printf("  %s\n", name)

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("create %s operation: %w", name, err)
	}

	return activities.NewServiceTask(name, op, activities.WithoutParams())
}

// awaitPayment builds a ReceiveTask that blocks forever in this demo — the
// payment message is never sent, so the timeout handler is what unblocks the
// scope.
func awaitPayment(name string) (*activities.ReceiveTask, error) {
	return activities.NewReceiveTask(name,
		bpmncommon.MustMessage("payment",
			data.MustItemDefinition(values.NewVariable(1))))
}

// timeoutTimer builds a Timer definition firing ~200ms after it arms — the
// interrupting Event Sub-Process's trigger.
func timeoutTimer() *events.TimerEventDefinition {
	return events.MustTimerEventDefinition(
		goexpr.Must(nil,
			data.MustItemDefinition(
				values.NewVariable(time.Now().Add(200*time.Millisecond))),
			func(_ context.Context, _ data.Source) (data.Value, error) {
				return values.NewVariable(
					time.Now().Add(200 * time.Millisecond)), nil
			}),
		nil, nil)
}

// adder is the shared Add surface of a Process and a SubProcess.
type adder interface {
	Add(flow.Element) error
}

// wire adds every element into c and links each [from, to] pair — the
// boilerplate the two composites share.
func wire(c adder, elems []flow.Element, pairs ...[2]flow.Element) error {
	for _, e := range elems {
		if err := c.Add(e); err != nil {
			return fmt.Errorf("add element: %w", err)
		}
	}

	for _, p := range pairs {
		if _, err := flow.Link(
			p[0].(flow.SequenceSource), p[1].(flow.SequenceTarget)); err != nil {
			return fmt.Errorf("link flow: %w", err)
		}
	}

	return nil
}
