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

// newHandler builds the conversation handler (SRD-017 phase-2c):
//
//	start("order placed", keyed by orderId)   <- instantiates + seeds the key
//	  -> await("payment received")            <- keyed in-instance receiver
//	  -> report(order_in + pay_in)            <- proves which payment arrived
//	  -> end
//
// The keyed message start spawns one handler per order and seeds its
// conversation key. The in-instance ReceiveTask subscribes keyed to that
// conversation, so the "payment received" message carrying the same order id
// routes back to the originating handler — not to any other. report pushes
// "<order>/<payment>" to done so the driver can verify there is no cross-talk.
func newHandler(done chan<- string) (*process.Process, error) {
	key, err := orderKey()
	if err != nil {
		return nil, err
	}

	proc, err := process.New("order-handler")
	if err != nil {
		return nil, fmt.Errorf("create handler process: %w", err)
	}

	start, err := events.NewStartEvent("order-received",
		events.WithMessageTrigger(events.MustMessageEventDefinition(
			bpmncommon.MustMessage(orderMsg, data.MustItemDefinition(
				values.NewVariable(""), foundation.WithID(orderItem))), nil)),
		events.WithCorrelationKey(key))
	if err != nil {
		return nil, fmt.Errorf("create message start: %w", err)
	}

	await, err := activities.NewReceiveTask("await-payment",
		bpmncommon.MustMessage(paymentMsg, data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID(payItem))),
		activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create payment receiver: %w", err)
	}

	reportOp, err := gooper.New("report-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			order, err := r.GetDataByID(orderItem)
			if err != nil {
				return nil, fmt.Errorf("read %q: %w", orderItem, err)
			}

			pay, err := r.GetDataByID(payItem)
			if err != nil {
				return nil, fmt.Errorf("read %q: %w", payItem, err)
			}

			done <- fmt.Sprintf("%v/%v",
				order.Value().Get(ctx), pay.Value().Get(ctx))

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

	for _, e := range []flow.Element{start, await, report, end} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	for _, l := range [][2]flow.Element{
		{start, await}, {await, report}, {report, end},
	} {
		if err := link(l[0], l[1]); err != nil {
			return nil, err
		}
	}

	return proc, nil
}

// link connects two flow elements with a sequence flow.
func link(src, trg flow.Element) error {
	s, ok := src.(flow.SequenceSource)
	if !ok {
		return fmt.Errorf("%q isn't a sequence source", src.Name())
	}

	t, ok := trg.(flow.SequenceTarget)
	if !ok {
		return fmt.Errorf("%q isn't a sequence target", trg.Name())
	}

	if _, err := flow.Link(s, t); err != nil {
		return fmt.Errorf("link %q -> %q: %w", src.Name(), trg.Name(), err)
	}

	return nil
}
