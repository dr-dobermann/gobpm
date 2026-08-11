package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// buildProcess assembles the SLA-monitored approval:
//
//	start → [approve-invoice] ──────────────────────────> end-approved
//	              ╎ (three NON-INTERRUPTING timer boundaries)
//	              ├╌ 50%  → [halfway]   → end-halfway
//	              ├╌ 90%  → [urgent]    → end-urgent
//	              └╌ 100% → [escalated] → end-escalated
//
// Each boundary carries a timeDuration ALONE — the relative one-shot timer form
// SRD-077 unlocked. They are separate bounded timers, not one recurring timer:
// 50/90/100 are not a uniform interval, so no cycle can express them.
func buildProcess(rec *notices) (*process.Process, error) {
	if err := data.CreateDefaultStates(); err != nil {
		return nil, fmt.Errorf("default states: %w", err)
	}

	proc, err := process.New("usertask-sla")
	if err != nil {
		return nil, fmt.Errorf("new process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	// A UserTask must declare at least one output (NewResource refuses an
	// empty parameter set), so `decision` is declared but NOT required — this
	// example is about the SLA clock, not about collecting a form. The
	// usertask example covers the rendered-output path.
	approve, err := activities.NewUserTask("approve-invoice",
		activities.WithCandidateUsers("operator"),
		activities.WithOutput("decision", "string", false),
		activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("approve task: %w", err)
	}

	endApproved, err := events.NewEndEvent("end-approved")
	if err != nil {
		return nil, fmt.Errorf("end-approved: %w", err)
	}

	elements := []flow.Element{start, approve, endApproved}
	links := [][2]flow.Element{{start, approve}, {approve, endApproved}}

	// One boundary + notification task + end event per SLA mark.
	for _, m := range slaMarks {
		boundary, err := slaBoundary(m.id, approve, m.at)
		if err != nil {
			return nil, err
		}

		task, err := noticeTask(rec, m.id, m.msg)
		if err != nil {
			return nil, err
		}

		end, err := events.NewEndEvent("end-" + m.id)
		if err != nil {
			return nil, fmt.Errorf("end-%s: %w", m.id, err)
		}

		elements = append(elements, boundary, task, end)
		links = append(links,
			[2]flow.Element{boundary, task},
			[2]flow.Element{task, end})
	}

	for _, e := range elements {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add %s: %w", e.ID(), err)
		}
	}

	for _, l := range links {
		if err := link(l[0], l[1]); err != nil {
			return nil, fmt.Errorf("link: %w", err)
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
