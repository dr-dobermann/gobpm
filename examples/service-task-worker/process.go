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
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
)

// buildOrderProcess builds, for one order amount:
//
//	start → reserve-stock → authorize-payment → «paymentStatus» gateway
//	                             │                    ├─ AUTHORIZED → shipped
//	                             │                    └─ else       → held
//	                             └─(PaymentGatewayDown boundary)→ payment-failed
//
// reserve-stock is worker-dispatched with an output mapping + a retry policy;
// authorize-payment is worker-dispatched, reads the bound «amount», and reports a
// Business Status (routed by the gateway) or a Business Error (caught by the
// boundary). No trust mode is set, so both resolve to WorkerTrusted.
func buildOrderProcess(name string, amount int) (*process.Process, error) {
	proc, err := process.New(name,
		data.WithProperties(
			data.MustProperty("amount",
				data.MustItemDefinition(values.NewVariable(amount),
					foundation.WithID("amount")),
				data.ReadyDataState)))
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	reserve, err := reserveTask()
	if err != nil {
		return nil, err
	}

	authorize, err := authorizeTask()
	if err != nil {
		return nil, err
	}

	gw, err := gateways.NewExclusiveGateway()
	if err != nil {
		return nil, fmt.Errorf("create gateway: %w", err)
	}

	shipped, err := events.NewEndEvent("shipped")
	if err != nil {
		return nil, fmt.Errorf("create shipped: %w", err)
	}

	held, err := events.NewEndEvent("held")
	if err != nil {
		return nil, fmt.Errorf("create held: %w", err)
	}

	// The interrupting Error boundary on authorize-payment catches the worker's
	// Business Error and routes to payment-failed.
	bpErr, err := bpmncommon.NewError("gateway-down", "PaymentGatewayDown", nil)
	if err != nil {
		return nil, fmt.Errorf("create error: %w", err)
	}

	eed, err := events.NewErrorEventDefinition(bpErr)
	if err != nil {
		return nil, fmt.Errorf("create error def: %w", err)
	}

	boundary, err := events.NewBoundaryEvent("pay-bnd", authorize, eed, true)
	if err != nil {
		return nil, fmt.Errorf("create boundary: %w", err)
	}

	failed, err := events.NewEndEvent("payment-failed")
	if err != nil {
		return nil, fmt.Errorf("create payment-failed: %w", err)
	}

	for _, e := range []flow.Element{
		start, reserve, authorize, gw, shipped, held, boundary, failed,
	} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	return proc, linkOrder(orderLinks{
		start: start, reserve: reserve, authorize: authorize, gw: gw,
		shipped: shipped, held: held, boundary: boundary, failed: failed,
	})
}

// orderLinks groups the process elements for linking.
type orderLinks struct {
	start     flow.SequenceSource
	reserve   *activities.ServiceTask
	authorize *activities.ServiceTask
	gw        *gateways.ExclusiveGateway
	shipped   flow.SequenceTarget
	held      flow.SequenceTarget
	boundary  flow.SequenceSource
	failed    flow.SequenceTarget
}

// linkOrder wires the sequence flows, including the gateway condition (route on
// the paymentStatus the worker wrote) and its default, and the boundary flow.
func linkOrder(l orderLinks) error {
	if _, err := flow.Link(l.start, l.reserve); err != nil {
		return fmt.Errorf("link start->reserve: %w", err)
	}

	if _, err := flow.Link(l.reserve, l.authorize); err != nil {
		return fmt.Errorf("link reserve->authorize: %w", err)
	}

	if _, err := flow.Link(l.authorize, l.gw); err != nil {
		return fmt.Errorf("link authorize->gw: %w", err)
	}

	if _, err := flow.Link(l.gw, l.shipped,
		flow.WithCondition(statusIs("AUTHORIZED"))); err != nil {
		return fmt.Errorf("link gw->shipped: %w", err)
	}

	held, err := flow.Link(l.gw, l.held)
	if err != nil {
		return fmt.Errorf("link gw->held: %w", err)
	}

	if err := l.gw.UpdateDefaultFlow(held); err != nil {
		return fmt.Errorf("set default flow: %w", err)
	}

	if _, err := flow.Link(l.boundary, l.failed); err != nil {
		return fmt.Errorf("link boundary->failed: %w", err)
	}

	return nil
}

// reserveTask is the worker-dispatched reserve-stock task: an output mapping that
// shapes the worker's {reservationId} body into a named variable, and a retry
// policy that lets the worker retry a transient inventory failure in-process.
func reserveTask() (*activities.ServiceTask, error) {
	st, err := activities.NewServiceTask("reserve-stock",
		service.MustOperation("reserve-op", nil, nil, nil),
		activities.WithWorker("reserve"),
		activities.WithOutputMapping(
			tasks.OutputRule{Path: bodyPath("body.reservationId"),
				Var: "reservationId"},
			tasks.OutputRule{Path: bodyPath("body.warehouse.zone"),
				Var: "warehouseZone"}),
		activities.WithRetryPolicy(tasks.FixedDelay(3, 300*time.Millisecond)),
		activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create reserve-stock: %w", err)
	}

	return st, nil
}

// authorizeTask is the worker-dispatched authorize-payment task: its operation
// binds the «amount» property as the worker's input, and WithStatus names the
// variable a Business Status verdict writes.
func authorizeTask() (*activities.ServiceTask, error) {
	amountIn := bpmncommon.MustMessage("amount-in",
		data.MustItemDefinition(values.NewVariable(0),
			foundation.WithID("amount")))

	st, err := activities.NewServiceTask("authorize-payment",
		service.MustOperation("authorize-op", amountIn, nil, nil),
		activities.WithWorker("authorize"),
		activities.WithStatus("paymentStatus", false),
		activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create authorize-payment: %w", err)
	}

	return st, nil
}

// bodyValue returns a rule Path that reads the worker response body's value.
// bodyPath extracts a value from the worker's response body by a structural
// path ("body.warehouse.zone") — the output mapping reaches INTO the structured
// body through the same resolver conditions and expressions use (ADR-011 v.6
// §2.9.2).
func bodyPath(path string) data.FormalExpression {
	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, path)
			if err != nil {
				return nil, err
			}

			return values.NewVariable(d.Value().Get(ctx)), nil
		})
}

// statusIs returns a gateway condition that is true when paymentStatus equals want.
func statusIs(want string) data.FormalExpression {
	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			v, err := ds.Find(ctx, "paymentStatus")
			if err != nil {
				return nil, err
			}

			got, _ := v.Value().Get(ctx).(string)

			return values.NewVariable(got == want), nil
		})
}
