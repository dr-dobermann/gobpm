package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// buildPanel builds the parallel Multi-Instance Sub-Process: one instance per
// reviewer, all run concurrently in distinct scopes. Each instance sees its
// reviewer name and assembles its `score` into the `scores` collection.
//
// A WithCompletionCondition(...) quorum could stop the panel early — canceling
// the not-yet-finished reviewers — but observing that truncation needs
// asynchronous reviewers (User/external tasks); the instant Service Tasks here
// all complete first.
func buildPanel() (*activities.SubProcess, error) {
	mi, err := activities.NewMultiInstance(
		// no WithSequential → parallel (the §13.3.7 default).
		activities.WithInputCollection("reviewers", "reviewer"),
		activities.WithOutputCollection("scores", "score"))
	if err != nil {
		return nil, fmt.Errorf("create multi-instance: %w", err)
	}

	panel, err := activities.NewSubProcess("panel", activities.WithLoop(mi))
	if err != nil {
		return nil, fmt.Errorf("create sub-process: %w", err)
	}

	pStart, err := events.NewStartEvent("p-start")
	if err != nil {
		return nil, fmt.Errorf("create p-start: %w", err)
	}

	score, err := scoreTask()
	if err != nil {
		return nil, err
	}

	pEnd, err := events.NewEndEvent("p-end")
	if err != nil {
		return nil, fmt.Errorf("create p-end: %w", err)
	}

	for _, e := range []flow.Element{pStart, score, pEnd} {
		if err := panel.Add(e); err != nil {
			return nil, fmt.Errorf("add panel element: %w", err)
		}
	}

	if _, err := flow.Link(pStart, score); err != nil {
		return nil, fmt.Errorf("link p-start->score: %w", err)
	}

	if _, err := flow.Link(score, pEnd); err != nil {
		return nil, fmt.Errorf("link score->p-end: %w", err)
	}

	return panel, nil
}
