package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
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

// orderRecord builds the structural "order" value:
//
//	{ id, total, items: [ {sku, price}, {sku, price} ] }
//
// a record of a scalar, a scalar, and a list of records (ADR-011 v.6 §2.9.1).
func orderRecord(total int) data.Value {
	item := func(sku string, price int) data.Value {
		return values.MustRecord(
			values.F("sku", values.NewVariable(sku)),
			values.F("price", values.NewVariable(price)))
	}

	return values.MustRecord(
		values.F("id", values.NewVariable("A-1")),
		values.F("total", values.NewVariable(total)),
		values.F("items", values.NewArray[data.Value](
			item("widget", 50), item("gadget", 100))),
	)
}

// buildProcess assembles: start → read-price → XOR{ order.total > 100 → premium
// | default → standard } → end. Both the service task and the gateway condition
// reach INTO the order record by path (order.items[0].price, order.total).
func buildProcess(total int) (*process.Process, error) {
	proc, err := process.New("structural-data",
		data.WithProperties(
			data.MustProperty("order",
				data.MustItemDefinition(orderRecord(total),
					foundation.WithID("order")),
				data.ReadyDataState)))
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	readPrice, err := pricePrinterTask()
	if err != nil {
		return nil, err
	}

	xor, err := gateways.NewExclusiveGateway()
	if err != nil {
		return nil, fmt.Errorf("create gateway: %w", err)
	}

	premium, err := printTask("premium", "  ▶ order.total > 100 → premium lane")
	if err != nil {
		return nil, err
	}

	standard, err := printTask("standard", "  ▶ default → standard lane")
	if err != nil {
		return nil, err
	}

	endP, err := events.NewEndEvent("end-premium")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	endS, err := events.NewEndEvent("end-standard")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{
		start, readPrice, xor, premium, standard, endP, endS,
	} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	links := []func() error{
		func() error { _, e := flow.Link(start, readPrice); return e },
		func() error { _, e := flow.Link(readPrice, xor); return e },
		func() error {
			_, e := flow.Link(xor, premium,
				flow.WithCondition(orderTotalGt100()))
			return e
		},
		func() error {
			df, e := flow.Link(xor, standard)
			if e != nil {
				return e
			}
			return xor.UpdateDefaultFlow(df)
		},
		func() error { _, e := flow.Link(premium, endP); return e },
		func() error { _, e := flow.Link(standard, endS); return e },
	}
	for _, link := range links {
		if err := link(); err != nil {
			return nil, fmt.Errorf("link flow: %w", err)
		}
	}

	return proc, nil
}

// orderTotalGt100 is the gateway condition — it reads the structural path
// "order.total" and branches on it (ADR-011 v.6 §2.9.2).
func orderTotalGt100() data.FormalExpression {
	return goexpr.Must(
		nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			v, err := ds.Find(ctx, "order.total")
			if err != nil {
				return nil, err
			}

			total, _ := v.Value().Get(ctx).(int)

			return values.NewVariable(total > 100), nil
		})
}

// pricePrinterTask reads order.items[0].price through the narrow DataReader —
// structural reach-in from in-process service code.
func pricePrinterTask() (*activities.ServiceTask, error) {
	op, err := gooper.New("read-first-price",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := r.GetData("order.items[0].price")
			if err != nil {
				return nil, fmt.Errorf("read order.items[0].price: %w", err)
			}

			fmt.Printf("  ▶ order.items[0].price = %v\n", d.Value().Get(ctx))

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("create read operation: %w", err)
	}

	task, err := activities.NewServiceTask("read-first-price", op,
		activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create read task: %w", err)
	}

	return task, nil
}

// printTask builds a ServiceTask whose Go functor prints msg.
func printTask(name, msg string) (*activities.ServiceTask, error) {
	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Println(msg)

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("create %s operation: %w", name, err)
	}

	task, err := activities.NewServiceTask(name, op, activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create %s task: %w", name, err)
	}

	return task, nil
}
