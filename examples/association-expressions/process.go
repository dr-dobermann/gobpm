package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	proc "github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// intItem and strItem are the two item definitions this process moves.
func intItem(id string, v int) *data.ItemDefinition {
	return data.MustItemDefinition(
		values.NewVariable(v), foundation.WithID(id))
}

func strItem(id, v string) *data.ItemDefinition {
	return data.MustItemDefinition(
		values.NewVariable(v), foundation.WithID(id))
}

// orderObject is the record the transformation READS from: {total: 120}.
// The expression addresses one field of it by path.
func orderObject() (*dataobjects.DataObject, error) {
	rec := values.MustRecord(values.F("total", values.NewVariable(120)))

	return dataobjects.New("order",
		data.MustItemDefinition(rec, foundation.WithID("order-item")),
		data.ReadyDataState)
}

// quoteObject is the record the assignment WRITES INTO: {status, amount}.
// It starts Unavailable because an association produces it — a data object
// fed by one is not readable until its producer writes it — and the
// assignment fills ONE of its fields, leaving the other alone.
func quoteObject() (*dataobjects.DataObject, error) {
	rec := values.MustRecord(
		values.F("status", values.NewVariable("pending")),
		values.F("clerk", values.NewVariable("ann")))

	return dataobjects.New("quote",
		data.MustItemDefinition(rec, foundation.WithID("quote-item")),
		data.ReadyDataState)
}

// chargeTask reads the amount its input association computed and produces a
// note. Its output feeds the assignment below.
func chargeTask() (*activities.ServiceTask, error) {
	op, err := gooper.New("charge",
		func(_ context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			amount, err := r.GetData("amount")
			if err != nil {
				return nil, fmt.Errorf("read amount: %w", err)
			}

			v := amount.Value().Get(context.Background())
			fmt.Printf("  charge: the transformation handed it %v\n", v)

			return strItem("note-item", fmt.Sprintf("charged %v", v)), nil
		})
	if err != nil {
		return nil, fmt.Errorf("charge operation: %w", err)
	}

	return activities.NewServiceTask("charge", op,
		activities.WithParameters(data.Input, data.MustParameter("amount",
			data.MustItemAwareElement(
				intItem("amount-item", 0), data.UnavailableDataState,
				foundation.WithID("amount-in")))),
		activities.WithParameters(data.Output, data.MustParameter("note",
			data.MustItemAwareElement(
				strItem("note-item", ""), data.UnavailableDataState))))
}

// reportTask reads the quote back out of scope and checks what the
// assignment did: ONE field written, the other untouched. It fails the
// instance when either is wrong, so the example asserts its own claim
// rather than only printing it.
func reportTask() (*activities.ServiceTask, error) {
	op, err := gooper.New("report",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			status, err := r.GetData("quote.status")
			if err != nil {
				return nil, fmt.Errorf("read quote.status: %w", err)
			}

			clerk, err := r.GetData("quote.clerk")
			if err != nil {
				return nil, fmt.Errorf("read quote.clerk: %w", err)
			}

			got := status.Value().Get(ctx)
			kept := clerk.Value().Get(ctx)

			fmt.Printf("  report: quote.status = %q (the assignment)\n", got)
			fmt.Printf("  report: quote.clerk  = %q (untouched)\n", kept)

			if got != "quoted: charged 240" {
				return nil, fmt.Errorf(
					"quote.status = %q, want the assignment's value", got)
			}

			if kept != "ann" {
				return nil, fmt.Errorf(
					"quote.clerk = %q, want it untouched — an assignment "+
						"writes at its path, not over the whole record", kept)
			}

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("report operation: %w", err)
	}

	return activities.NewServiceTask("report", op, activities.WithoutParams())
}

// buildProcess assembles start → charge → report → end, with the two
// association shapes wired onto the one task:
//
//	order, rate ──transformation──▶ charge.amount     (order.total * rate)
//	charge.note ──assignment──────▶ quote.status      (a field, not the whole)
func buildProcess() (*proc.Process, error) {
	order, err := orderObject()
	if err != nil {
		return nil, fmt.Errorf("order data object: %w", err)
	}

	rate, err := dataobjects.New("rate", intItem("rate-item", 2),
		data.ReadyDataState)
	if err != nil {
		return nil, fmt.Errorf("rate data object: %w", err)
	}

	quote, err := quoteObject()
	if err != nil {
		return nil, fmt.Errorf("quote data object: %w", err)
	}

	charge, err := chargeTask()
	if err != nil {
		return nil, err
	}

	if werr := wireInput(order, rate, charge); werr != nil {
		return nil, werr
	}

	if werr := wireOutput(quote, charge); werr != nil {
		return nil, werr
	}

	report, err := reportTask()
	if err != nil {
		return nil, err
	}

	return assemble(order, rate, quote, charge, report)
}
