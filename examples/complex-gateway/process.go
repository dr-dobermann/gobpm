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

// buildProcess assembles the Complex-gateway approval diamond (ADR-005 §2.11):
//
//	start → AND-split ─┬→ manager ─┐
//	                   ├→ finance ─┼ Complex join → finalize → end
//	                   └→ cfo ─────┘
//
// The parallel split runs all three approvers; the Complex join fires on the
// activation rule [(amount<1000, 2), (amount>=1000, 3)] — two approvals suffice
// for a small order, all three for a large — consuming any later approval as a
// trailing token (partial-join / discriminator family, WCP-30 / WCP-9).
func buildProcess(amount int) (*process.Process, error) {
	proc, err := process.New("order-approval",
		data.WithProperties(
			data.MustProperty("amount",
				data.MustItemDefinition(
					values.NewVariable(amount), foundation.WithID("amount")),
				data.ReadyDataState)))
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	split, err := gateways.NewParallelGateway()
	if err != nil {
		return nil, fmt.Errorf("create AND-split: %w", err)
	}

	join, err := approvalJoin()
	if err != nil {
		return nil, err
	}

	finalize, err := printTask("finalize", "  ✓ order finalized")
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	approvers, err := approverTasks()
	if err != nil {
		return nil, err
	}

	elems := append(
		[]flow.Element{start, split, join, finalize, end}, approvers...)
	for _, e := range elems {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	return proc, wire(start, split, join, finalize, end, approvers)
}

// approvalJoin builds the Complex join with the data-aware activation rule.
func approvalJoin() (*gateways.ComplexGateway, error) {
	small, err := gateways.NewTriple(2,
		gateways.WithGuard(amountCond(func(a int) bool { return a < 1000 })))
	if err != nil {
		return nil, fmt.Errorf("small-order triple: %w", err)
	}

	big, err := gateways.NewTriple(3,
		gateways.WithGuard(amountCond(func(a int) bool { return a >= 1000 })))
	if err != nil {
		return nil, fmt.Errorf("large-order triple: %w", err)
	}

	cg, err := gateways.NewComplexGateway(
		gateways.WithActivation(small, big),
		gateways.WithDirection(gateways.Converging))
	if err != nil {
		return nil, fmt.Errorf("create complex join: %w", err)
	}

	return cg, nil
}

// approverTasks builds the three parallel approver service tasks.
func approverTasks() ([]flow.Element, error) {
	tasks := []flow.Element{}

	for _, name := range []string{"manager", "finance", "cfo"} {
		t, err := printTask(name, "  ▶ "+name+" approved")
		if err != nil {
			return nil, err
		}

		tasks = append(tasks, t)
	}

	return tasks, nil
}

// wire links start → split → each approver → join → finalize → end.
func wire(
	start, split, join, finalize, end flow.Element, approvers []flow.Element,
) error {
	links := [][2]flow.Element{{start, split}}
	for _, ap := range approvers {
		links = append(links, [2]flow.Element{split, ap}, [2]flow.Element{ap, join})
	}

	links = append(links,
		[2]flow.Element{join, finalize}, [2]flow.Element{finalize, end})

	for _, l := range links {
		src, trg := l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget)
		if _, err := flow.Link(src, trg); err != nil {
			return fmt.Errorf("link: %w", err)
		}
	}

	return nil
}

// amountCond builds a bool condition over the "amount" property.
func amountCond(pred func(a int) bool) data.FormalExpression {
	return goexpr.Must(
		nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			v, err := ds.Find(ctx, "amount")
			if err != nil {
				return nil, err
			}

			a, _ := v.Value().Get(ctx).(int)

			return values.NewVariable(pred(a)), nil
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
