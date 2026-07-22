package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// buildBoard builds the parallel Multi-Instance review board: one instance per
// reviewer, all voting concurrently. A Complex behavior throws a "quorum-reached"
// signal on every completion once numberOfCompletedInstances ≥ 2. It returns the
// board and the signal definition the boundary catches (the same signal, matched by
// name).
func buildBoard() (*activities.SubProcess, flow.EventDefinition, error) {
	sig, err := events.NewSignal("quorum-reached", nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create signal: %w", err)
	}

	throwDef, err := events.NewSignalEventDefinition(sig)
	if err != nil {
		return nil, nil, fmt.Errorf("create throw def: %w", err)
	}

	catchDef, err := events.NewSignalEventDefinition(sig)
	if err != nil {
		return nil, nil, fmt.Errorf("create catch def: %w", err)
	}

	quorum, err := events.NewImplicitThrowEvent("quorum", throwDef)
	if err != nil {
		return nil, nil, fmt.Errorf("create implicit throw: %w", err)
	}

	cbd, err := activities.NewComplexBehaviorDefinition(completedAtLeast(2), quorum)
	if err != nil {
		return nil, nil, fmt.Errorf("create complex behavior: %w", err)
	}

	mi, err := activities.NewMultiInstance(
		activities.WithInputCollection("reviewers", "reviewer"),
		activities.WithBehavior(activities.BehaviorComplex),
		activities.WithComplexBehavior(cbd))
	if err != nil {
		return nil, nil, fmt.Errorf("create multi-instance: %w", err)
	}

	board, err := activities.NewSubProcess("board", activities.WithLoop(mi))
	if err != nil {
		return nil, nil, fmt.Errorf("create sub-process: %w", err)
	}

	bStart, err := events.NewStartEvent("b-start")
	if err != nil {
		return nil, nil, fmt.Errorf("create b-start: %w", err)
	}

	vote, err := voteTask()
	if err != nil {
		return nil, nil, err
	}

	bEnd, err := events.NewEndEvent("b-end")
	if err != nil {
		return nil, nil, fmt.Errorf("create b-end: %w", err)
	}

	for _, e := range []flow.Element{bStart, vote, bEnd} {
		if err := board.Add(e); err != nil {
			return nil, nil, fmt.Errorf("add board element: %w", err)
		}
	}

	if _, err := flow.Link(bStart, vote); err != nil {
		return nil, nil, fmt.Errorf("link b-start->vote: %w", err)
	}

	if _, err := flow.Link(vote, bEnd); err != nil {
		return nil, nil, fmt.Errorf("link vote->b-end: %w", err)
	}

	return board, catchDef, nil
}
