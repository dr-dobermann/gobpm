package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// buildProcess assembles the on-page loop: start → throw"repeat", and
// catch"repeat" → work → XOR{ count<3 → throw"repeat" (back) | default → end }.
// The two throws (initial + back-edge) are Link sources pairing to the one
// catch target — many sources → one target (§10.5.1) — so each iteration the
// token redirects through the catch into the work task until the count exits.
func buildProcess(count *int) (*process.Process, error) {
	proc, err := process.New("link-events")
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

	throwInit, err := linkThrow("throw-init", "repeat")
	if err != nil {
		return nil, err
	}

	throwBack, err := linkThrow("throw-back", "repeat")
	if err != nil {
		return nil, err
	}

	catchLoop, err := linkCatch("catch-loop", "repeat")
	if err != nil {
		return nil, err
	}

	work, err := workTask(count)
	if err != nil {
		return nil, err
	}

	xor, cond, err := loopGateway(count)
	if err != nil {
		return nil, err
	}

	for _, e := range []flow.Element{
		start, throwInit, catchLoop, work, xor, throwBack, end,
	} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	// start → throw"repeat" ; catch"repeat" → work → xor (the loop body)
	for _, l := range [][2]flow.Element{
		{start, throwInit}, {catchLoop, work}, {work, xor},
	} {
		if _, err := flow.Link(l[0].(flow.SequenceSource),
			l[1].(flow.SequenceTarget)); err != nil {
			return nil, fmt.Errorf("link flow: %w", err)
		}
	}

	// xor -[count<3]-> throw"repeat" (back-edge) ; xor -default-> end
	if _, err := flow.Link(xor, throwBack, flow.WithCondition(cond)); err != nil {
		return nil, fmt.Errorf("link back-edge: %w", err)
	}

	df, err := flow.Link(xor, end)
	if err != nil {
		return nil, fmt.Errorf("link default: %w", err)
	}

	return proc, xor.UpdateDefaultFlow(df)
}
