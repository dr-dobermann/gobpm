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
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
)

// buildProcess assembles: start → quote → read-back → end.
//
// quote is worker-dispatched; its output mapping turns the worker's FLAT body
// into one nested «order» record — a record field, plus a two-element list built
// by auto-vivify — then read-back reaches into that assembled value by path.
func buildProcess() (*process.Process, error) {
	proc, err := process.New("structural-output-mapping")
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	quote, err := quoteTask()
	if err != nil {
		return nil, err
	}

	readBack, err := readBackTask()
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{start, quote, readBack, end} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	links := []func() error{
		func() error { _, e := flow.Link(start, quote); return e },
		func() error { _, e := flow.Link(quote, readBack); return e },
		func() error { _, e := flow.Link(readBack, end); return e },
	}
	for _, link := range links {
		if err := link(); err != nil {
			return nil, fmt.Errorf("link flow: %w", err)
		}
	}

	return proc, nil
}

// quoteTask is the worker-dispatched task whose output mapping ASSEMBLES a nested
// «order» record from the worker's flat body: three rules sharing the head
// "order" produce one output value — a record with a total and an items list —
// instead of three flat variables (ADR-011 v.6 §2.9.5, assembly-by-head).
func quoteTask() (*activities.ServiceTask, error) {
	st, err := activities.NewServiceTask("quote",
		service.MustOperation("quote-op", nil, nil, nil),
		activities.WithWorker("quote"),
		activities.WithOutputMapping(
			tasks.OutputRule{Path: bodyPath("body.total"), Var: "order.total"},
			tasks.OutputRule{Path: bodyPath("body.price0"),
				Var: "order.items[0].price"},
			tasks.OutputRule{Path: bodyPath("body.price1"),
				Var: "order.items[1].price"}),
		activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create quote task: %w", err)
	}

	return st, nil
}

// readBackTask reads the assembled order.items[1].price through the narrow
// DataReader — proof the flat body became a navigable nested value.
func readBackTask() (*activities.ServiceTask, error) {
	op, err := gooper.New("read-back",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := r.GetData("order.items[1].price")
			if err != nil {
				return nil, fmt.Errorf("read order.items[1].price: %w", err)
			}

			fmt.Printf("  ▶ order.items[1].price = %v\n", d.Value().Get(ctx))

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("create read-back operation: %w", err)
	}

	task, err := activities.NewServiceTask("read-back", op,
		activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("create read-back task: %w", err)
	}

	return task, nil
}

// bodyPath reads a value from the worker's response body by path — the mapping's
// source side, reaching into the body through the resolver conditions use.
func bodyPath(path string) data.FormalExpression {
	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(0)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, path)
			if err != nil {
				return nil, err
			}

			return values.NewVariable(d.Value().Get(ctx)), nil
		})
}
