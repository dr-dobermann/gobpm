package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// intValue wraps an int as a process value.
func intValue(v int) data.Value { return values.NewVariable(v) }

// buildProcess assembles the order flow with the fulfillment fragment as
// an embedded sub-process:
//
//	start → accept → fulfil[ start → pick → pack → end ] → notify → end
//
// The fragment runs in its own scope: pick/pack read the parent's
// order-id through the walk-up, their scratch data stays scoped, and the
// parent resumes only when the fragment drains (BPMN §13.3.4).
func buildProcess() (*process.Process, error) {
	fulfil, err := activities.NewSubProcess("fulfil")
	if err != nil {
		return nil, fmt.Errorf("create sub-process: %w", err)
	}

	fStart, err := events.NewStartEvent("f-start")
	if err != nil {
		return nil, fmt.Errorf("create f-start: %w", err)
	}

	pick, err := step("pick", "picked", 1)
	if err != nil {
		return nil, err
	}

	pack, err := step("pack", "", 0)
	if err != nil {
		return nil, err
	}

	fEnd, err := events.NewEndEvent("f-end")
	if err != nil {
		return nil, fmt.Errorf("create f-end: %w", err)
	}

	for _, e := range []flow.Element{fStart, pick, pack, fEnd} {
		if err := fulfil.Add(e); err != nil {
			return nil, fmt.Errorf("add into fulfil: %w", err)
		}
	}

	proc, err := process.New("embedded-subprocess",
		data.WithProperties(
			data.MustProperty("order-id",
				data.MustItemDefinition(intValue(4711),
					foundation.WithID("order-id")),
				data.ReadyDataState)))
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	accept, err := step("accept", "", 0)
	if err != nil {
		return nil, err
	}

	notify, err := step("notify", "", 0)
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{start, accept, fulfil, notify, end} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	links := []func() error{
		func() error { _, e := flow.Link(fStart, pick); return e },
		func() error { _, e := flow.Link(pick, pack); return e },
		func() error { _, e := flow.Link(pack, fEnd); return e },
		func() error { _, e := flow.Link(start, accept); return e },
		func() error { _, e := flow.Link(accept, fulfil); return e },
		func() error { _, e := flow.Link(fulfil, notify); return e },
		func() error { _, e := flow.Link(notify, end); return e },
	}
	for _, link := range links {
		if err := link(); err != nil {
			return nil, fmt.Errorf("link flow: %w", err)
		}
	}

	return proc, nil
}
