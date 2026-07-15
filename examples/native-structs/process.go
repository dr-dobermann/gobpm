package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/adapters"
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

// Order is the HOST's own type — the engine navigates it live, through the
// gobpm tags (ADR-011 v.6 §2.9.5): no conversion, no parallel model.
type Order struct {
	ID     string `gobpm:"id"`
	Total  int    `gobpm:"total"`
	Items  []Item `gobpm:"items"`
	Secret string `gobpm:"-"` // never visible to the process
}

// Item is a nested host type — slice elements surface as live sub-records.
type Item struct {
	SKU   string `gobpm:"sku"`
	Price int    `gobpm:"price"`
}

// Receipt is the host type the tasks commit — the commit-diff runs over the
// wrapped instances and reports per-path DataChange facts.
type Receipt struct {
	Sum int `gobpm:"sum"`
}

// buildProcess assembles start → quote → reprice → XOR{order.total > 100 →
// premium | default → standard}; the condition reaches INTO the wrapped host
// struct by path.
func buildProcess(order data.Value) (*process.Process, error) {
	proc, err := process.New("native-structs",
		data.WithProperties(
			data.MustProperty("order",
				data.MustItemDefinition(order, foundation.WithID("order")),
				data.ReadyDataState)))
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	quote, err := receiptTask("quote", 5)
	if err != nil {
		return nil, err
	}

	reprice, err := receiptTask("reprice", 6)
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
		start, quote, reprice, xor, premium, standard, endP, endS,
	} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	links := []func() error{
		func() error { _, e := flow.Link(start, quote); return e },
		func() error { _, e := flow.Link(quote, reprice); return e },
		func() error { _, e := flow.Link(reprice, xor); return e },
		func() error {
			_, e := flow.Link(xor, premium, flow.WithCondition(totalGt100()))
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

// totalGt100 reads the structural path "order.total" — straight into the
// host's live struct.
func totalGt100() data.FormalExpression {
	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "order.total")
			if err != nil {
				return nil, err
			}

			total, _ := d.Value().Get(ctx).(int)

			return values.NewVariable(total > 100), nil
		})
}

// receiptTask commits a WRAPPED host Receipt — the commit-diff sees it as an
// ordinary record.
func receiptTask(name string, sum int) (*activities.ServiceTask, error) {
	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Printf("  %s → commit wrapped Receipt{Sum:%d}\n", name, sum)

			return data.MustItemDefinition(
				adapters.MustWrap(&Receipt{Sum: sum}),
				foundation.WithID("receipt")), nil
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
