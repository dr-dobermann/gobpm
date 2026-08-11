package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	bpmncommon "github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// buildProcess assembles start ─> throw"order" ─> catch"order" ─> confirm ─> end.
// done receives the payload the confirm task read back out of scope, so the
// caller can assert the message actually carried it.
func buildProcess(done chan<- string) (*process.Process, error) {
	// the payload the throw event reads from scope and publishes.
	proc, err := process.New("message-events-demo",
		data.WithProperties(
			data.MustProperty("order_out",
				data.MustItemDefinition(values.NewVariable(orderID),
					foundation.WithID("order_out")),
				data.ReadyDataState)))
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	throw, err := events.NewIntermediateThrowEvent("throw-order",
		events.MustMessageEventDefinition(
			bpmncommon.MustMessage("order placed",
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID("order_out"))),
			nil))
	if err != nil {
		return nil, fmt.Errorf("create throw event: %w", err)
	}

	catch, err := events.NewIntermediateCatchEvent("catch-order",
		events.MustMessageEventDefinition(
			bpmncommon.MustMessage("order placed",
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID("order_in"))),
			nil))
	if err != nil {
		return nil, fmt.Errorf("create catch event: %w", err)
	}

	// confirm reads the bound payload from scope and signals completion.
	confirmOp, err := gooper.New("confirm-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			got, readErr := r.GetDataByID("order_in")
			if readErr != nil {
				return nil, fmt.Errorf("read order_in: %w", readErr)
			}

			done <- fmt.Sprintf("%v", got.Value().Get(ctx))

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

	for _, e := range []flow.Element{start, throw, catch, confirm, end} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	for _, l := range [][2]flow.Element{
		{start, throw}, {throw, catch}, {catch, confirm}, {confirm, end},
	} {
		if err := link(l[0], l[1]); err != nil {
			return nil, err
		}
	}

	return proc, nil
}
