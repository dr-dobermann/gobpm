package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

const (
	orderMessage   = "order placed"
	paymentMessage = "payment received"
	orderItem      = "order_in"
	paymentItem    = "payment_in"
)

// buildProcess builds a fulfillment process started by a PARALLEL instantiating
// Event-Based gateway: the gate has no incoming flow, so the FIRST of its two
// correlated messages creates the instance and the other re-arms keyed to it;
// the instance completes only once BOTH have arrived (BPMN §13.2).
func buildProcess() (*process.Process, error) {
	proc, err := process.New("order-fulfillment")
	if err != nil {
		return nil, fmt.Errorf("new process: %w", err)
	}

	key, err := orderKey()
	if err != nil {
		return nil, err
	}

	gate, err := gateways.NewEventBasedGateway(
		gateways.WithInstantiate(),
		gateways.WithEventGatewayType(gateways.ParallelEvents),
		gateways.WithCorrelationKey(key))
	if err != nil {
		return nil, fmt.Errorf("gate: %w", err)
	}

	orderArm, err := messageCatch("await-order", orderMessage, orderItem)
	if err != nil {
		return nil, err
	}

	payArm, err := messageCatch("await-payment", paymentMessage, paymentItem)
	if err != nil {
		return nil, err
	}

	recordOrder, err := printTask("record-order",
		"  ✓ order placed   → recorded")
	if err != nil {
		return nil, err
	}

	recordPay, err := printTask("record-payment",
		"  ✓ payment received → recorded")
	if err != nil {
		return nil, err
	}

	endO, err := events.NewEndEvent("end-order")
	if err != nil {
		return nil, fmt.Errorf("end-order: %w", err)
	}

	endP, err := events.NewEndEvent("end-payment")
	if err != nil {
		return nil, fmt.Errorf("end-payment: %w", err)
	}

	for _, e := range []flow.Element{
		gate, orderArm, payArm, recordOrder, recordPay, endO, endP,
	} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add %s: %w", e.ID(), err)
		}
	}

	for _, l := range [][2]flow.Element{
		{gate, orderArm}, {orderArm, recordOrder}, {recordOrder, endO},
		{gate, payArm}, {payArm, recordPay}, {recordPay, endP},
	} {
		if _, err := flow.Link(
			l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget)); err != nil {
			return nil, fmt.Errorf("link: %w", err)
		}
	}

	return proc, nil
}

// orderKey is the gate's CorrelationKey: one property reading the order id from
// EITHER arm's message payload, so whichever message arrives first derives the
// same key and the second routes back to the instance it created.
func orderKey() (*bpmncommon.CorrelationKey, error) {
	orderRE, err := retrieve(orderMessage, orderItem)
	if err != nil {
		return nil, err
	}

	payRE, err := retrieve(paymentMessage, paymentItem)
	if err != nil {
		return nil, err
	}

	prop, err := bpmncommon.NewCorrelationProperty("orderId", "string",
		[]bpmncommon.CorrelationPropertyRetrievalExpression{orderRE, payRE})
	if err != nil {
		return nil, fmt.Errorf("correlation property: %w", err)
	}

	key, err := bpmncommon.NewCorrelationKey("orderKey",
		[]bpmncommon.CorrelationProperty{*prop})
	if err != nil {
		return nil, fmt.Errorf("correlation key: %w", err)
	}

	return key, nil
}

// retrieve builds the per-message retrieval expression that reads the order id
// (the payload bound at itemID) for message msgName.
func retrieve(
	msgName, itemID string,
) (bpmncommon.CorrelationPropertyRetrievalExpression, error) {
	expr := goexpr.Must(nil, data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, itemID)
			if err != nil {
				return nil, err
			}

			return values.NewVariable(fmt.Sprint(d.Value().Get(ctx))), nil
		})

	re, err := bpmncommon.NewCorrelationPropertyRetrievalExpression(expr,
		bpmncommon.MustMessage(msgName, data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID(itemID))))
	if err != nil {
		return bpmncommon.CorrelationPropertyRetrievalExpression{},
			fmt.Errorf("retrieval expression %q: %w", msgName, err)
	}

	return *re, nil
}

func messageCatch(
	id, msgName, itemID string,
) (*events.IntermediateCatchEvent, error) {
	def, err := events.NewMessageEventDefinition(
		bpmncommon.MustMessage(msgName, data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID(itemID))),
		nil)
	if err != nil {
		return nil, fmt.Errorf("message def %q: %w", msgName, err)
	}

	ice, err := events.NewIntermediateCatchEvent(id, def)
	if err != nil {
		return nil, fmt.Errorf("catch %q: %w", id, err)
	}

	return ice, nil
}

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
