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

// buildProcess wires start → board → end and attaches a non-interrupting boundary
// to the board that catches the "quorum-reached" behavior signal and runs a
// notification side-flow (boundary → notify → notify-end).
func buildProcess() (*process.Process, error) {
	proc, err := process.New("multi-instance-behavior",
		data.WithProperties(data.MustProperty("reviewers",
			data.MustItemDefinition(
				values.NewArray("Ann", "Bob", "Cara"),
				foundation.WithID("reviewers")),
			data.ReadyDataState)))
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	board, catchDef, err := buildBoard()
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	boundary, notify, notifyEnd, err := buildNotification(board, catchDef)
	if err != nil {
		return nil, err
	}

	for _, e := range []flow.Element{start, board, end, boundary, notify, notifyEnd} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	if _, err := flow.Link(start, board); err != nil {
		return nil, fmt.Errorf("link start->board: %w", err)
	}

	if _, err := flow.Link(board, end); err != nil {
		return nil, fmt.Errorf("link board->end: %w", err)
	}

	if _, err := flow.Link(boundary, notify); err != nil {
		return nil, fmt.Errorf("link boundary->notify: %w", err)
	}

	if _, err := flow.Link(notify, notifyEnd); err != nil {
		return nil, fmt.Errorf("link notify->notify-end: %w", err)
	}

	return proc, nil
}
