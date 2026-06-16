package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// newConsumer builds the handler process B:
//
//	(order placed) ─▶ report ─▶ end
//
// Its start event is a correlation-keyed message start: the engine
// auto-instantiates one handler per distinct order key (no instance exists
// until a message arrives). The report ServiceTask reads the bound payload and
// pushes the order id to done — one send per born instance.
func newConsumer(done chan<- string) (*process.Process, error) {
	key, err := orderKey("order_in")
	if err != nil {
		return nil, err
	}

	proc, err := process.New("order-handler")
	if err != nil {
		return nil, fmt.Errorf("create handler process: %w", err)
	}

	start, err := events.NewStartEvent("order-received",
		events.WithMessageTrigger(events.MustMessageEventDefinition(
			bpmncommon.MustMessage(messageName,
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID("order_in"))),
			nil)),
		events.WithCorrelationKey(key))
	if err != nil {
		return nil, fmt.Errorf("create message start: %w", err)
	}

	reportOp, err := gooper.New("report-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			got, err := r.GetDataByID("order_in")
			if err != nil {
				return nil, fmt.Errorf("read order_in: %w", err)
			}

			done <- fmt.Sprint(got.Value().Get(ctx))

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("create report operation: %w", err)
	}

	report, err := activities.NewServiceTask("report", reportOp,
		activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create report task: %w", err)
	}

	end, err := events.NewEndEvent("handled")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{start, report, end} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	for _, l := range [][2]flow.Element{{start, report}, {report, end}} {
		if err := link(l[0], l[1]); err != nil {
			return nil, err
		}
	}

	return proc, nil
}
