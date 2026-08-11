package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// buildProcess assembles
//
//	start ─> send-order ─> receive-order ─> confirm ─> end
//
// done is closed when the confirm task has run, and received is where it puts
// what it read back — so the caller can assert the message carried its payload
// rather than merely arriving.
func buildProcess(done chan struct{}, received *string) (*process.Process, error) {
	// the payload the SendTask reads from the instance scope and publishes.
	proc, err := process.New("message-demo",
		data.WithProperties(
			data.MustProperty("order_out",
				data.MustItemDefinition(
					values.NewVariable(orderID),
					foundation.WithID("order_out")),
				data.ReadyDataState)))
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	// SendTask: binds the "order_out" property and publishes it as the
	// "order placed" message.
	send, err := activities.NewSendTask("send-order",
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_out"))),
		activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create send task: %w", err)
	}

	// ReceiveTask: waits for the "order placed" message and binds its payload
	// into "order_in", which the output association lands in the DataObject.
	outParam := data.MustParameter("received order",
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_in")),
			data.UnavailableDataState))

	receive, err := activities.NewReceiveTask("receive-order",
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_in"))),
		activities.WithParameters(data.Output, outParam))
	if err != nil {
		return nil, fmt.Errorf("create receive task: %w", err)
	}

	receivedDO, err := dataobjects.New("received-order",
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID("order_in")),
		nil)
	if err != nil {
		return nil, fmt.Errorf("create result object: %w", err)
	}

	if err = receivedDO.AssociateSource(
		receive, []string{"order_in"}, nil); err != nil {
		return nil, fmt.Errorf("bind result object: %w", err)
	}

	// confirm: a trailing ServiceTask that reads the received-order DataObject
	// from the per-instance scope by name (via its DataReader) and signals
	// completion — so main reads the value without racing the engine and
	// without touching the shared model object (SRD-063: a DataObject's value
	// lives in the per-instance scope, not on the definition object).
	confirmOp, err := gooper.New("confirm-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, derr := r.GetData("received-order")
			if derr != nil {
				return nil, fmt.Errorf("read received-order: %w", derr)
			}

			*received, _ = d.Value().Get(ctx).(string)
			close(done)

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("create confirm operation: %w", err)
	}

	confirm, err := activities.NewServiceTask("confirm", confirmOp,
		activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create confirm task: %w", err)
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{start, send, receive, confirm, end, receivedDO} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	for _, l := range [][2]flow.Element{
		{start, send}, {send, receive}, {receive, confirm}, {confirm, end},
	} {
		if err := link(l[0], l[1]); err != nil {
			return nil, err
		}
	}

	return proc, nil
}
