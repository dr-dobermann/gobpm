package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// buildProcess assembles the escalation demo — a review-order sub-process that
// raises a non-critical OVER_BUDGET escalation, and an interrupting Escalation
// boundary that catches it by code and routes to notify-manager (so the run
// ends at end-escalated, not end-approved):
//
//	start → [review-order] ─────────────────────> end-approved
//	             (raise OVER_BUDGET, Escalation End Event)
//	             ╳ (escalation boundary OVER_BUDGET, interrupting)
//	             └─> [notify-manager] ──────────> end-escalated
func buildProcess(ran *pathSet) (*process.Process, error) {
	proc, err := process.New("escalation-events")
	if err != nil {
		return nil, fmt.Errorf("new process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	body, err := reviewOrder()
	if err != nil {
		return nil, err
	}

	approved, err := events.NewEndEvent("end-approved")
	if err != nil {
		return nil, fmt.Errorf("end-approved: %w", err)
	}

	def, err := escDef()
	if err != nil {
		return nil, err
	}

	boundary, err := events.NewBoundaryEvent("over-budget", body, def, true)
	if err != nil {
		return nil, fmt.Errorf("boundary: %w", err)
	}

	notify, err := notifyManager(ran)
	if err != nil {
		return nil, err
	}

	escalated, err := events.NewEndEvent("end-escalated")
	if err != nil {
		return nil, fmt.Errorf("end-escalated: %w", err)
	}

	for _, e := range []flow.Element{
		start, body, approved, boundary, notify, escalated,
	} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add %s: %w", e.ID(), err)
		}
	}

	for _, l := range [][2]flow.Element{
		{start, body},
		{body, approved},
		{boundary, notify},
		{notify, escalated},
	} {
		if err := link(l[0], l[1]); err != nil {
			return nil, err
		}
	}

	return proc, nil
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
