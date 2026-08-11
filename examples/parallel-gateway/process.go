package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// buildProcess assembles the demonstrated shape:
//
//	start ─> split ─┬─> worker-a ─┬─> join ─> end
//	                └─> worker-b ─┘
//
// done is handed to each worker so the branch reports its own execution.
func buildProcess(done chan<- string) (*process.Process, error) {
	proc, err := process.New("parallel-demo")
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	split, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Diverging))
	if err != nil {
		return nil, fmt.Errorf("create split: %w", err)
	}

	workerA, err := newWorker("worker-a", done)
	if err != nil {
		return nil, err
	}

	workerB, err := newWorker("worker-b", done)
	if err != nil {
		return nil, err
	}

	join, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Converging))
	if err != nil {
		return nil, fmt.Errorf("create join: %w", err)
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	for _, e := range []flow.Element{start, split, workerA, workerB, join, end} {
		if addErr := proc.Add(e); addErr != nil {
			return nil, fmt.Errorf("add element: %w", addErr)
		}
	}

	for _, l := range [][2]flow.Element{
		{start, split},
		{split, workerA}, {split, workerB},
		{workerA, join}, {workerB, join},
		{join, end},
	} {
		if linkErr := link(l[0], l[1]); linkErr != nil {
			return nil, linkErr
		}
	}

	return proc, nil
}
