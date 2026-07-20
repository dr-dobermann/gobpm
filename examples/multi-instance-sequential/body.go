package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// buildOrdersBody builds the Multi-Instance Sub-Process: one instance per
// element of the `amounts` collection, run sequentially. Each instance sees its
// element as `amount` and assembles its `withTax` output into the `taxed`
// collection.
func buildOrdersBody() (*activities.SubProcess, error) {
	mi, err := activities.NewMultiInstance(
		activities.WithSequential(),
		activities.WithInputCollection("amounts", "amount"),
		activities.WithOutputCollection("taxed", "withTax"))
	if err != nil {
		return nil, fmt.Errorf("create multi-instance: %w", err)
	}

	body, err := activities.NewSubProcess("orders", activities.WithLoop(mi))
	if err != nil {
		return nil, fmt.Errorf("create sub-process: %w", err)
	}

	bStart, err := events.NewStartEvent("b-start")
	if err != nil {
		return nil, fmt.Errorf("create b-start: %w", err)
	}

	tax, err := taxTask()
	if err != nil {
		return nil, err
	}

	bEnd, err := events.NewEndEvent("b-end")
	if err != nil {
		return nil, fmt.Errorf("create b-end: %w", err)
	}

	for _, e := range []flow.Element{bStart, tax, bEnd} {
		if err := body.Add(e); err != nil {
			return nil, fmt.Errorf("add body element: %w", err)
		}
	}

	if _, err := flow.Link(bStart, tax); err != nil {
		return nil, fmt.Errorf("link b-start->tax: %w", err)
	}

	if _, err := flow.Link(tax, bEnd); err != nil {
		return nil, fmt.Errorf("link tax->b-end: %w", err)
	}

	return body, nil
}
