package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// buildProcess models start → approve (parallel MI over three reviewers) →
// report → end.
func buildProcess() (*process.Process, error) {
	reviewers := data.MustProperty("reviewers",
		data.MustItemDefinition(values.NewArray[any]("alice", "bob", "carol"),
			foundation.WithID("reviewers")),
		data.ReadyDataState)

	p, err := process.New("multi-instance-human",
		foundation.WithID("mi-human"), data.WithProperties(reviewers))
	if err != nil {
		return nil, err
	}

	start, err := events.NewStartEvent("start", foundation.WithID("start"))
	if err != nil {
		return nil, err
	}

	approve, err := buildApproval()
	if err != nil {
		return nil, err
	}

	report, err := buildReport()
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end", foundation.WithID("end"))
	if err != nil {
		return nil, err
	}

	for _, e := range []flow.Element{start, approve, report, end} {
		if aerr := p.Add(e); aerr != nil {
			return nil, aerr
		}
	}

	if _, err = flow.Link(start, approve); err != nil {
		return nil, err
	}

	if _, err = flow.Link(approve, report); err != nil {
		return nil, err
	}

	if _, err = flow.Link(report, end); err != nil {
		return nil, err
	}

	return p, nil
}

// buildApproval is the fan-out: one iteration per reviewer, all offered at
// once, each completed on its own.
//
// The RESULT MAP is what makes the outcome readable. Without a declared
// strategy the default is last-wins, so which reviewer's decision survived
// would depend on who happened to answer last. Keyed by reviewer, every
// answer is kept — and the key is evaluated in the completing iteration's own
// frame, so it can name the person whose approval it was.
func buildApproval() (*activities.UserTask, error) {
	byReviewer := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, src data.Source) (data.Value, error) {
			d, err := src.Find(ctx, "reviewer")
			if err != nil {
				return nil, fmt.Errorf("read reviewer: %w", err)
			}

			return values.NewVariable(d.Value().Get(ctx)), nil
		})

	mi, err := activities.NewMultiInstance(
		activities.WithInputCollection("reviewers", "reviewer"),
		activities.WithResultMap("decisions", "decision", byReviewer))
	if err != nil {
		return nil, err
	}

	// EACH ITERATION RESOLVES ITS OWN ASSIGNEE, in its own data context: the
	// task belongs to the reviewer that iteration was seeded with, and nobody
	// else may complete it. The same expression, three answers — which is
	// what a fan-out over human work is for.
	return activities.NewUserTask("approve",
		activities.WithAssigneeExpr(assigneeIsTheReviewer()),
		activities.WithOutput("decision", "string", true),
		activities.WithoutParams(), activities.WithLoop(mi),
		foundation.WithID("approve"))
}

// assigneeIsTheReviewer reads the element this iteration was given.
func assigneeIsTheReviewer() data.FormalExpression {
	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, src data.Source) (data.Value, error) {
			d, err := src.Find(ctx, "reviewer")
			if err != nil {
				return nil, fmt.Errorf("read reviewer: %w", err)
			}

			return values.NewVariable(d.Value().Get(ctx)), nil
		})
}

// buildReport reads, from a node AFTER the activity, what that activity did —
// which is the question BPMN's numberOf* counts cannot answer, because they
// end with the activation they describe.
func buildReport() (*activities.ServiceTask, error) {
	op, err := reportOperation()
	if err != nil {
		return nil, err
	}

	return activities.NewServiceTask("report", op,
		activities.WithoutParams(), foundation.WithID("report"))
}
