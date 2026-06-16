// Command message-intermediate-events demonstrates BPMN message events
// (SRD-014 / ADR-014 v.1): an IntermediateThrowEvent publishes a message to the
// engine's MessageBroker, and downstream an IntermediateCatchEvent waits for it
// (through a MessageWaiter) and binds the payload into scope — the event-shaped
// peers of the SendTask/ReceiveTask.
//
//	start ─> throw-order ─> catch-order ─> confirm ─> end
//	         (publishes      (waits on the   (reads the
//	          "order          broker, binds   bound payload)
//	          placed")        the payload)
//
// The events live on one track, so the throw completes before the catch
// subscribes; the in-memory broker buffers the published message until then.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

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
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

const orderID = "ORD-2026-002"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("create default states: %w", err)
	}

	engine, err := thresher.New("message-events-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	// the payload the throw event reads from scope and publishes.
	proc, err := process.New("message-events-demo",
		data.WithProperties(
			data.MustProperty("order_out",
				data.MustItemDefinition(values.NewVariable(orderID),
					foundation.WithID("order_out")),
				data.ReadyDataState)))
	if err != nil {
		return fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return fmt.Errorf("create start: %w", err)
	}

	throw, err := events.NewIntermediateThrowEvent("throw-order",
		events.MustMessageEventDefinition(
			bpmncommon.MustMessage("order placed",
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID("order_out"))),
			nil))
	if err != nil {
		return fmt.Errorf("create throw event: %w", err)
	}

	catch, err := events.NewIntermediateCatchEvent("catch-order",
		events.MustMessageEventDefinition(
			bpmncommon.MustMessage("order placed",
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID("order_in"))),
			nil))
	if err != nil {
		return fmt.Errorf("create catch event: %w", err)
	}

	// confirm reads the bound payload from scope and signals completion.
	done := make(chan string, 1)

	confirmOp, err := gooper.New("confirm-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			got, err := r.GetDataByID("order_in")
			if err != nil {
				return nil, fmt.Errorf("read order_in: %w", err)
			}

			done <- fmt.Sprintf("%v", got.Value().Get(ctx))

			return nil, nil
		})
	if err != nil {
		return fmt.Errorf("create confirm operation: %w", err)
	}

	confirm, err := activities.NewServiceTask("confirm", confirmOp,
		activities.WithoutParams())
	if err != nil {
		return fmt.Errorf("create confirm task: %w", err)
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{start, throw, catch, confirm, end} {
		if err := proc.Add(e); err != nil {
			return fmt.Errorf("add element: %w", err)
		}
	}

	for _, l := range [][2]flow.Element{
		{start, throw}, {throw, catch}, {catch, confirm}, {confirm, end},
	} {
		if err := link(l[0], l[1]); err != nil {
			return err
		}
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

	var got string
	select {
	case got = <-done:
	case <-ctx.Done():
		return fmt.Errorf("timed out waiting for the message-event round-trip")
	}

	if got != orderID {
		return fmt.Errorf("catch-order: want %q, got %q", orderID, got)
	}

	fmt.Printf("  ✓ throw-order published %q\n", orderID)
	fmt.Printf("  ✓ catch-order bound it; confirm read order_in = %q\n", got)
	fmt.Println("✓ message-events-demo completed: the message travelled the " +
		"broker from the throw event to the catch event")

	return nil
}

// link wires src -> trg with a sequence flow.
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
