package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// wireInput builds the TRANSFORMATION shape: two sources — order and rate —
// combined by an expression whose result REPLACES the task's input
// (BPMN §10.4.2 rule 1). Several sources are legal precisely because a
// transformation is present, and the expression reads one of them by
// structural path (order.total), through the same resolver a gateway
// condition reads.
func wireInput(
	order, rate *dataobjects.DataObject, charge *activities.ServiceTask,
) error {
	amount := goexpr.Must(nil, intItem("amount-item", 0),
		func(ctx context.Context, src data.Source) (data.Value, error) {
			total, err := src.Find(ctx, "order.total")
			if err != nil {
				return nil, fmt.Errorf("read order.total: %w", err)
			}

			r, err := src.Find(ctx, "rate")
			if err != nil {
				return nil, fmt.Errorf("read rate: %w", err)
			}

			t, ok := total.Value().Get(ctx).(int)
			if !ok {
				return nil, fmt.Errorf("order.total is not an int")
			}

			n, ok := r.Value().Get(ctx).(int)
			if !ok {
				return nil, fmt.Errorf("rate is not an int")
			}

			return values.NewVariable(t * n), nil
		})

	// order attaches the association; rate joins it as a second source.
	//
	// AssociateTargetInput, not AssociateTarget: the by-item form picks the
	// node input whose itemDefinition equals the source's, and under a
	// transformation the two ends deliberately differ — a record's field
	// times an int, landing in an int. Naming the input directly is the
	// attach that fits (SRD-094 FR-7 added it for events, for the same
	// reason: the ends do not share an item).
	return order.AssociateTargetInput(
		charge, "amount-in", amount, data.WithSource(rate.ItemAware()))
}

// wireOutput builds the ASSIGNMENT shape: the task's note is written into
// ONE FIELD of the order record (§10.4.2 rule 2). The `to` path is
// absolute — its head names the association's target — and everything
// after the head addresses inside it, so `status` is replaced while
// `total` is left alone.
func wireOutput(
	quote *dataobjects.DataObject, charge *activities.ServiceTask,
) error {
	from := goexpr.Must(nil, strItem("status-item", ""),
		func(ctx context.Context, src data.Source) (data.Value, error) {
			note, err := src.Find(ctx, "note")
			if err != nil {
				return nil, fmt.Errorf("read note: %w", err)
			}

			text, ok := note.Value().Get(ctx).(string)
			if !ok {
				return nil, fmt.Errorf("note is not a string")
			}

			return values.NewVariable("quoted: " + text), nil
		})

	assign, err := data.NewAssignment(from, "quote.status")
	if err != nil {
		return fmt.Errorf("assignment: %w", err)
	}

	return quote.AssociateSource(charge, []string{"note-item"}, nil,
		data.WithAssignments(assign))
}

// assemble wraps the task as start → charge → end and adds the data.
func assemble(
	order, rate, quote *dataobjects.DataObject,
	charge, report *activities.ServiceTask,
) (*process.Process, error) {
	proc, err := process.New("association-expressions")
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{
		start, charge, report, end, order, rate, quote,
	} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add %q: %w", e.Name(), err)
		}
	}

	if _, err := flow.Link(start, charge); err != nil {
		return nil, fmt.Errorf("link start→charge: %w", err)
	}

	if _, err := flow.Link(charge, report); err != nil {
		return nil, fmt.Errorf("link charge→report: %w", err)
	}

	if _, err := flow.Link(report, end); err != nil {
		return nil, fmt.Errorf("link report→end: %w", err)
	}

	return proc, nil
}
