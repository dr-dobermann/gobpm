package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

const calcKey = "tax-calc"

// buildCallee assembles the reusable child: start → tax(reads "subtotal",
// writes "total" = subtotal + 20%) → end. It is registered once and invoked by
// any caller — the reuse boundary (ADR-023 §2.7).
func buildCallee() (*process.Process, error) {
	p, err := process.New("tax-calc", foundation.WithID(calcKey))
	if err != nil {
		return nil, err
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, err
	}

	op, err := gooper.New("tax",
		func(ctx context.Context, ds service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, readErr := ds.GetData("subtotal")
			if readErr != nil {
				return nil, readErr
			}

			sub, _ := d.Value().Get(ctx).(int)
			fmt.Printf("    (child) subtotal=%d → total=%d\n", sub, sub+sub/5)

			return data.MustItemDefinition(values.NewVariable(sub+sub/5),
				foundation.WithID("total")), nil
		})
	if err != nil {
		return nil, err
	}

	tax, err := activities.NewServiceTask("tax", op, activities.WithoutParams())
	if err != nil {
		return nil, err
	}

	return p, wire(p, start, tax, mustEnd())
}

// buildCaller assembles start → checkout[calls tax-calc] → show → end, seeding
// "subtotal" and reading the child's "total" back.
func buildCaller(got *seen, subtotal int) (*process.Process, error) {
	p, err := process.New("checkout",
		data.WithProperties(prop("subtotal", subtotal)))
	if err != nil {
		return nil, err
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, err
	}

	call, err := activities.NewCallActivity("charge", calcKey,
		activities.WithParameters(data.Input, param("subtotal")),
		activities.WithParameters(data.Output, param("total")))
	if err != nil {
		return nil, err
	}

	show, err := showTask(got)
	if err != nil {
		return nil, err
	}

	return p, wire(p, start, call, show, mustEnd())
}

// showTask builds the ServiceTask that prints the child's "total" the loop
// committed into the caller's scope.
func showTask(got *seen) (*activities.ServiceTask, error) {
	op, err := gooper.New("show",
		func(ctx context.Context, ds service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := ds.GetData("total")
			if err != nil {
				return nil, err
			}

			total := d.Value().Get(ctx)
			got.record(total)

			fmt.Printf("  ✓ caller sees total=%v\n", total)

			return nil, nil
		})
	if err != nil {
		return nil, err
	}

	return activities.NewServiceTask("show", op, activities.WithoutParams())
}

// prop builds a Ready int process property.
func prop(name string, v int) *data.Property {
	return data.MustProperty(name,
		data.MustItemDefinition(values.NewVariable(v),
			foundation.WithID(name)),
		data.ReadyDataState)
}

// param builds a declared call parameter (the name is the contract).
func param(name string) *data.Parameter {
	return data.MustParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(0),
				foundation.WithID(name)),
			data.ReadyDataState))
}

// mustEnd builds an end event or panics (example brevity).
func mustEnd() *events.EndEvent {
	e, err := events.NewEndEvent("end")
	if err != nil {
		panic(err)
	}

	return e
}

// wire adds the nodes to p and links them in sequence.
func wire(p *process.Process, nodes ...flow.Element) error {
	for _, n := range nodes {
		if err := p.Add(n); err != nil {
			return err
		}
	}

	for i := 0; i+1 < len(nodes); i++ {
		if err := link(nodes[i], nodes[i+1]); err != nil {
			return err
		}
	}

	return nil
}

// link connects two flow elements with a sequence flow, reporting an element
// that cannot carry one rather than panicking on the assertion.
func link(src, trg flow.Element) error {
	s, ok := src.(flow.SequenceSource)
	if !ok {
		return fmt.Errorf("%q is not a sequence source", src.Name())
	}

	t, ok := trg.(flow.SequenceTarget)
	if !ok {
		return fmt.Errorf("%q is not a sequence target", trg.Name())
	}

	if _, err := flow.Link(s, t); err != nil {
		return fmt.Errorf("link: %w", err)
	}

	return nil
}
