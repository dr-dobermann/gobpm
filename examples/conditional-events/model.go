package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// buildProcess assembles the order-total watcher:
//
//	start → prep → { addItems (total=140) → end-shop,
//	                 watch[total>100]     → notify → end-notify }
//
// The watch branch parks on the conditional catch (total starts at 20);
// addItems' committed total=140 re-evaluates the condition and the
// false→true edge releases the notify path — data-driven waiting without
// polling.
func buildProcess() (*process.Process, error) {
	proc, err := process.New("conditional-events",
		data.WithProperties(
			data.MustProperty("total",
				data.MustItemDefinition(values.NewVariable(20),
					foundation.WithID("total")),
				data.ReadyDataState)))
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	prep, err := commitTask("prep", "cart", 1)
	if err != nil {
		return nil, err
	}

	addItems, err := commitTask("addItems", "total", 140)
	if err != nil {
		return nil, err
	}

	cond, err := totalAbove(100)
	if err != nil {
		return nil, fmt.Errorf("create watch condition: %w", err)
	}

	watch, err := events.NewIntermediateCatchEvent("watch-total",
		events.MustConditionalEventDefinition(cond))
	if err != nil {
		return nil, fmt.Errorf("create watch catch: %w", err)
	}

	notify, err := notifyTask()
	if err != nil {
		return nil, err
	}

	endShop, err := events.NewEndEvent("end-shop")
	if err != nil {
		return nil, fmt.Errorf("create end-shop: %w", err)
	}

	endNotify, err := events.NewEndEvent("end-notify")
	if err != nil {
		return nil, fmt.Errorf("create end-notify: %w", err)
	}

	for _, e := range []flow.Element{
		start, prep, addItems, watch, notify, endShop, endNotify,
	} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	links := []func() error{
		func() error { _, e := flow.Link(start, prep); return e },
		func() error { _, e := flow.Link(prep, addItems); return e },
		func() error { _, e := flow.Link(prep, watch); return e },
		func() error { _, e := flow.Link(addItems, endShop); return e },
		func() error { _, e := flow.Link(watch, notify); return e },
		func() error { _, e := flow.Link(notify, endNotify); return e },
	}
	for _, link := range links {
		if err := link(); err != nil {
			return nil, fmt.Errorf("link flow: %w", err)
		}
	}

	return proc, nil
}
