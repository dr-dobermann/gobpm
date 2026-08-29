package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// amounts are the orders the Multi-Instance walks, in order, and withTax is
// the rule the task applies to each. Both the process data and the example's
// own expectation read them, so the two cannot drift apart.
var amounts = []int{100, 250, 80}

func withTax(amount int) int { return amount + amount/5 } // +20%

// amountList returns the amounts as the `any` slice the array value wants.
func amountList() []any {
	l := make([]any, 0, len(amounts))
	for _, a := range amounts {
		l = append(l, a)
	}

	return l
}

// wantTaxed is what the output collection must hold: one taxed figure per
// amount, in input order.
func wantTaxed() []int {
	w := make([]int, len(amounts))
	for i, a := range amounts {
		w[i] = withTax(a)
	}

	return w
}

// sameInts reports an error unless got holds exactly the ints in want, in
// order. The values arrive as `any` from the collection, so a wrong TYPE is a
// mismatch too, not a silent zero.
func sameInts(got []any, want []int) error {
	if len(got) != len(want) {
		return fmt.Errorf("got %d values %v, want %d %v",
			len(got), got, len(want), want)
	}

	for i, w := range want {
		if got[i] != w {
			return fmt.Errorf("got %v, want %v", got, want)
		}
	}

	return nil
}

// buildProcess wires start → orders → end, seeding the input `amounts`
// collection the Multi-Instance activity iterates over.
func buildProcess() (*process.Process, error) {
	proc, err := process.New("multi-instance-sequential",
		data.WithProperties(data.MustProperty("amounts",
			data.MustItemDefinition(values.NewArray(amountList()...),
				foundation.WithID("amounts")),
			data.ReadyDataState)))
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	orders, err := buildOrdersBody()
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{start, orders, end} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	if _, err := flow.Link(start, orders); err != nil {
		return nil, fmt.Errorf("link start->orders: %w", err)
	}

	if _, err := flow.Link(orders, end); err != nil {
		return nil, fmt.Errorf("link orders->end: %w", err)
	}

	return proc, nil
}

// reportTaxed prints the assembled `taxed` output collection the Multi-Instance
// published once every iteration completed (the visibility barrier).
func reportTaxed(ctx context.Context, r service.DataReader) error {
	taxed, err := r.GetData("taxed")
	if err != nil {
		return fmt.Errorf("read taxed collection: %w", err)
	}

	col, ok := taxed.Value().(data.Collection)
	if !ok {
		return fmt.Errorf("taxed is not a collection")
	}

	got := col.GetAll(ctx)

	// Sequential Multi-Instance keeps input ORDER: the output collection must
	// hold each amount's taxed figure in the position its input had. A
	// parallel run, a lost instance, or an output assembled out of order would
	// all print a plausible-looking list and still exit 0 — so the exact
	// sequence is what gets checked, not just the length.
	if err := sameInts(got, wantTaxed()); err != nil {
		return fmt.Errorf("output collection: %w", err)
	}

	fmt.Printf("\n  completed — taxed amounts: %v\n", got)

	return nil
}
