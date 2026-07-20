package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// escalationCode names the escalation the order review raises and the boundary
// catches. Matching is by code, so throw and catch use the same value.
const escalationCode = "OVER_BUDGET"

// escDef builds an EscalationEventDefinition carrying escalationCode — used on
// both the throw (inside the sub-process) and the catch (the boundary).
func escDef() (*events.EscalationEventDefinition, error) {
	esc, err := events.NewEscalation("over-budget", escalationCode,
		data.MustItemDefinition(values.NewVariable(0)))
	if err != nil {
		return nil, fmt.Errorf("escalation: %w", err)
	}

	return events.NewEscalationEventDefinition(esc)
}

// reviewOrder builds the guarded sub-process:
//
//	sub-start → [raise OVER_BUDGET] (Escalation End Event)
//
// The Escalation End Event raises a non-critical escalation and ends its token.
// Unlike an Error End Event it does NOT fault the instance; the enclosing
// boundary catches it by code (or, unhandled, it would simply be logged).
func reviewOrder() (*activities.SubProcess, error) {
	body, err := activities.NewSubProcess("review-order")
	if err != nil {
		return nil, fmt.Errorf("sub-process: %w", err)
	}

	start, err := events.NewStartEvent("sub-start")
	if err != nil {
		return nil, fmt.Errorf("sub-start: %w", err)
	}

	def, err := escDef()
	if err != nil {
		return nil, err
	}

	throw, err := events.NewEndEvent("raise-over-budget",
		events.WithEscalationTrigger(def))
	if err != nil {
		return nil, fmt.Errorf("throw: %w", err)
	}

	for _, e := range []flow.Element{start, throw} {
		if err := body.Add(e); err != nil {
			return nil, fmt.Errorf("add %s: %w", e.ID(), err)
		}
	}

	if _, err := flow.Link(start, throw); err != nil {
		return nil, fmt.Errorf("link sub-start→throw: %w", err)
	}

	return body, nil
}
