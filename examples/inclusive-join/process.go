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
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// buildProcess assembles the OR diamond (ADR-005 §2.9/§2.10):
//
//	start → OR-split ─┬ >1000 → manager-review ┐
//	                  ├ >500  → fraud-check     ┼→ OR-join → finalize → end
//	                  └ <100  → fast-track      ┘
//
// The Inclusive split forks every branch whose condition is true (the true
// subset); the Inclusive join waits for exactly that subset — a never-taken
// branch is found unreachable and does not stall it — then continues once.
func buildProcess(amount int) (*process.Process, error) {
	proc, err := process.New("order-or-diamond",
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

	split, err := gateways.NewInclusiveGateway(
		gateways.WithDirection(gateways.Diverging))
	if err != nil {
		return nil, fmt.Errorf("create OR-split: %w", err)
	}

	join, err := gateways.NewInclusiveGateway(
		gateways.WithDirection(gateways.Converging))
	if err != nil {
		return nil, fmt.Errorf("create OR-join: %w", err)
	}

	mgr, err := printTask("manager-review", "  ▶ amount > 1000 → manager review")
	if err != nil {
		return nil, err
	}

	fraud, err := printTask("fraud-check", "  ▶ amount > 500 → fraud check")
	if err != nil {
		return nil, err
	}

	fast, err := printTask("fast-track", "  ▶ amount < 100 → fast-tracked")
	if err != nil {
		return nil, err
	}

	finalize, err := printTask("finalize", "  ✓ branches merged → order finalized")
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{
		start, split, join, mgr, fraud, fast, finalize, end,
	} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	return proc, wire(split, join, branches{
		start: start, mgr: mgr, fraud: fraud, fast: fast,
		finalize: finalize, end: end,
	})
}

// branches groups the diamond's non-gateway nodes for wiring.
type branches struct {
	start, mgr, fraud, fast, finalize flow.Element
	end                               flow.Element
}

// wire links the diamond: start → split, the three conditional split branches,
// each branch → join, and join → finalize → end.
func wire(split, join *gateways.InclusiveGateway, b branches) error {
	links := []struct {
		from, to flow.Element
		cond     data.FormalExpression
	}{
		{b.start, split, nil},
		{split, b.mgr, amountAbove(1000)},
		{split, b.fraud, amountAbove(500)},
		{split, b.fast, amountBelow(100)},
		{b.mgr, join, nil},
		{b.fraud, join, nil},
		{b.fast, join, nil},
		{join, b.finalize, nil},
		{b.finalize, b.end, nil},
	}

	for _, l := range links {
		opts := []options.Option{}
		if l.cond != nil {
			opts = append(opts, flow.WithCondition(l.cond))
		}

		src := l.from.(flow.SequenceSource)
		trg := l.to.(flow.SequenceTarget)

		if _, err := flow.Link(src, trg, opts...); err != nil {
			return fmt.Errorf("link: %w", err)
		}
	}

	return nil
}

// amountAbove / amountBelow build a bool condition over the "amount" property.
func amountAbove(n int) data.FormalExpression {
	return amountCond(func(a int) bool { return a > n })
}

func amountBelow(n int) data.FormalExpression {
	return amountCond(func(a int) bool { return a < n })
}

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
