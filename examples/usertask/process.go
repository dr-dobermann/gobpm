package main

import (
	"bytes"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/hinteraction/consinp"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// buildProcess builds start -> UserTask("approve") -> end. The UserTask is
// claimable by the "operator" candidate and collects a "decision" output through
// a console renderer (consinp) that is fed a scripted answer, so the example runs
// end-to-end without interactive input.
func buildProcess() (*process.Process, error) {
	if err := data.CreateDefaultStates(); err != nil {
		return nil, err
	}

	form, err := consinp.NewRenderer(
		consinp.WithStringInput("decision", "Approve? type a decision"),
		consinp.WithSource(bytes.NewBufferString("approved\n")),
	)
	if err != nil {
		return nil, err
	}

	ut, err := activities.NewUserTask("approve",
		activities.WithCandidateUsers("operator"),
		activities.WithRenderer(form),
		activities.WithOutput("decision", "string", true),
		activities.WithoutParams(),
	)
	if err != nil {
		return nil, err
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, err
	}

	p, err := process.New("approval")
	if err != nil {
		return nil, err
	}

	for _, e := range []flow.Element{start, ut, end} {
		if err := p.Add(e); err != nil {
			return nil, err
		}
	}

	if _, err := flow.Link(start, ut); err != nil {
		return nil, err
	}

	if _, err := flow.Link(ut, end); err != nil {
		return nil, err
	}

	return p, nil
}
