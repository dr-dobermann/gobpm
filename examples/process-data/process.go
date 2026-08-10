package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// demo is the built model plus the two per-branch DataObjects the run reads
// its results back from.
type demo struct {
	proc    *process.Process
	resultA *dataobjects.DataObject
	resultB *dataobjects.DataObject
}

// buildProcess assembles:
//
//	start ─> split ─┬─> greet-a ─> end-a       (result-a DataObject)
//	                └─> greet-b ─> end-b       (result-b DataObject)
//
// done is handed to each greeter so a branch reports its own execution.
func buildProcess(done chan<- string) (*demo, error) {
	proc, err := process.New("data-demo",
		data.WithProperties(
			data.MustProperty("user_name",
				data.MustItemDefinition(
					values.NewVariable("dr.Dobermann"),
					foundation.WithID("user_name")),
				data.ReadyDataState)))
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

	greetA, resultA, err := newGreeter("greet-a", "res-a", "Hello", done)
	if err != nil {
		return nil, err
	}

	greetB, resultB, err := newGreeter("greet-b", "res-b", "Welcome", done)
	if err != nil {
		return nil, err
	}

	endA, err := events.NewEndEvent("end-a")
	if err != nil {
		return nil, fmt.Errorf("create end-a: %w", err)
	}

	endB, err := events.NewEndEvent("end-b")
	if err != nil {
		return nil, fmt.Errorf("create end-b: %w", err)
	}

	// Register the DataObjects on the Process too, so each instance seeds them
	// into its own scope and the branch results flow into the per-instance
	// objects (SRD-063).
	for _, e := range []flow.Element{
		start, split, greetA, greetB, endA, endB, resultA, resultB,
	} {
		if addErr := proc.Add(e); addErr != nil {
			return nil, fmt.Errorf("add element: %w", addErr)
		}
	}

	for _, l := range [][2]flow.Element{
		{start, split},
		{split, greetA}, {split, greetB},
		{greetA, endA}, {greetB, endB},
	} {
		if linkErr := link(l[0], l[1]); linkErr != nil {
			return nil, err
		}
	}
	return &demo{proc: proc, resultA: resultA, resultB: resultB}, nil
}
