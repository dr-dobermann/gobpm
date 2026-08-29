package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// answer does what a person would: opens the task, claims it so nobody else
// can, and submits a decision.
//
// It acts as the reviewer the announcement named — the assignee this iteration
// resolved for itself. The Claim is not ceremony: completion is strict, so
// only the holder may complete, and a second reader racing for the same task
// is refused up front rather than at submit time.
func (i *inbox) answer(ctx context.Context, taskID, who string) {
	actor := reviewer{id: who}

	if _, err := i.eng.Take(ctx, taskID, actor); err != nil {
		fmt.Printf("  take failed for %s: %v\n", who, err)

		return
	}

	if err := i.eng.Claim(ctx, taskID, actor); err != nil {
		fmt.Printf("  claim failed for %s: %v\n", who, err)

		return
	}

	decision := "approved"
	if who == "carol" {
		decision = "rejected"
	}

	out := []data.Data{
		data.MustParameter("decision",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable(decision)),
				data.ReadyDataState)),
	}

	if err := i.eng.Complete(ctx, taskID, actor, out); err != nil {
		fmt.Printf("  complete failed for %s: %v\n", who, err)

		return
	}

	fmt.Printf("  %s answered %q\n", who, decision)
}
