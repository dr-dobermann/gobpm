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

// buildProcess assembles: start → XOR{ amount > 1000 → manager-review |
// default → auto-approve } → end. The exclusive gateway routes by the "amount"
// property — data-based branching (ADR-005 §2.8).
func buildProcess(amount int) (*process.Process, error) {
	proc, err := process.New("order-routing",
		data.WithProperties(
			data.MustProperty("amount",
				data.MustItemDefinition(
					values.NewVariable(amount),
					foundation.WithID("amount")),
				data.ReadyDataState)))
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	xor, err := gateways.NewExclusiveGateway()
	if err != nil {
		return nil, fmt.Errorf("create gateway: %w", err)
	}

	review, err := printTask("manager-review",
		"  ▶ amount > 1000 → routed to manager review")
	if err != nil {
		return nil, err
	}

	approve, err := printTask("auto-approve",
		"  ▶ amount ≤ 1000 → auto-approved")
	if err != nil {
		return nil, err
	}

	endR, err := events.NewEndEvent("end-review")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	endA, err := events.NewEndEvent("end-approve")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{start, xor, review, approve, endR, endA} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	if _, err := flow.Link(start, xor); err != nil {
		return nil, fmt.Errorf("link start->xor: %w", err)
	}

	if _, err := flow.Link(xor, review,
		flow.WithCondition(amountGt1000())); err != nil {
		return nil, fmt.Errorf("link xor->review: %w", err)
	}

	df, err := flow.Link(xor, approve)
	if err != nil {
		return nil, fmt.Errorf("link xor->approve: %w", err)
	}

	if err := xor.UpdateDefaultFlow(df); err != nil {
		return nil, fmt.Errorf("set default flow: %w", err)
	}

	if _, err := flow.Link(review, endR); err != nil {
		return nil, fmt.Errorf("link review->end: %w", err)
	}

	if _, err := flow.Link(approve, endA); err != nil {
		return nil, fmt.Errorf("link approve->end: %w", err)
	}

	return proc, nil
}

// amountGt1000 is the gateway's data-based condition: amount > 1000.
func amountGt1000() data.FormalExpression {
	return goexpr.Must(
		nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			v, err := ds.Find(ctx, "amount")
			if err != nil {
				return nil, err
			}

			amount, _ := v.Value().Get(ctx).(int)

			return values.NewVariable(amount > 1000), nil
		})
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
