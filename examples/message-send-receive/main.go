// Command message-send-receive demonstrates broker-backed message handling
// (SRD-013 / ADR-014 v.1): a SendTask binds a process property and publishes it
// to the engine's MessageBroker; downstream a ReceiveTask waits for that message
// through a MessageWaiter, and on arrival binds the payload into scope, where an
// output association lands it in a DataObject.
//
//	start ─> send-order ─> receive-order ─> confirm ─> end
//	         (publishes      (waits on the      (signals
//	          "order         broker, binds       completion)
//	          placed")        the payload)
//
// The tasks live on one track, so the send completes before the receive
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
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

const orderID = "ORD-2026-001"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("create default states: %w", err)
	}

	engine, err := thresher.New("message-demo-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	// the payload the SendTask reads from the instance scope and publishes.
	proc, err := process.New("message-demo",
		data.WithProperties(
			data.MustProperty("order_out",
				data.MustItemDefinition(
					values.NewVariable(orderID),
					foundation.WithID("order_out")),
				data.ReadyDataState)))
	if err != nil {
		return fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return fmt.Errorf("create start: %w", err)
	}

	// SendTask: binds the "order_out" property and publishes it as the
	// "order placed" message.
	send, err := activities.NewSendTask("send-order",
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_out"))),
		activities.WithoutParams())
	if err != nil {
		return fmt.Errorf("create send task: %w", err)
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
		return fmt.Errorf("create receive task: %w", err)
	}

	receivedDO, err := dataobjects.New("received-order",
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID("order_in")),
		nil)
	if err != nil {
		return fmt.Errorf("create result object: %w", err)
	}

	if err := receivedDO.AssociateSource(
		receive, []string{"order_in"}, nil); err != nil {
		return fmt.Errorf("bind result object: %w", err)
	}

	// confirm: a trailing ServiceTask that signals completion once the receive
	// has resumed, so main can read the DataObject without racing the engine.
	done := make(chan struct{})

	confirmOp, err := gooper.New("confirm-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			close(done)

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

	for _, e := range []flow.Element{start, send, receive, confirm, end} {
		if err := proc.Add(e); err != nil {
			return fmt.Errorf("add element: %w", err)
		}
	}

	for _, l := range [][2]flow.Element{
		{start, send}, {send, receive}, {receive, confirm}, {confirm, end},
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

	select {
	case <-done:
	case <-ctx.Done():
		return fmt.Errorf("timed out waiting for the message round-trip")
	}

	// brief grace for the receive producer stage to commit into the DataObject.
	time.Sleep(200 * time.Millisecond)

	got, ok := receivedDO.Subject().Structure().Get(context.Background()).(string)
	if !ok || got != orderID {
		return fmt.Errorf("received-order: want %q, got %v", orderID, got)
	}

	fmt.Printf("  ✓ send-order published %q\n", orderID)
	fmt.Printf("  ✓ receive-order bound it into received-order = %q\n", got)
	fmt.Println("✓ message-demo completed: the message travelled the broker " +
		"from the SendTask to the ReceiveTask")

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
