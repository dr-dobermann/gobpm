package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// checkRun asserts the properties the construct exists for, so a regression
// fails the example rather than printing something plausible.
func checkRun(
	ctx context.Context, box *inbox, r service.DataReader,
) error {
	if err := checkDistinctTasks(box); err != nil {
		return err
	}

	return checkDecisions(ctx, r)
}

// checkDistinctTasks: three iterations announce THREE tasks, not one between
// them. That is the property the construct was refused over — with a single
// shared identity only one was addressable, and the rest completed without
// anyone doing them.
func checkDistinctTasks(box *inbox) error {
	offered := box.offered()

	seen := map[string]bool{}
	for _, id := range offered {
		seen[id] = true
	}

	if len(seen) != 3 {
		return fmt.Errorf(
			"want 3 distinct tasks offered, got %d (from %d announcements)",
			len(seen), len(offered))
	}

	return nil
}

// checkDecisions: every reviewer's answer is kept, under that reviewer's name.
// The undeclared default is last-wins, so without the declared map exactly one
// of the three would have survived — and which one would depend on who
// happened to answer last.
func checkDecisions(ctx context.Context, r service.DataReader) error {
	want := map[string]string{
		"alice": "approved",
		"bob":   "approved",
		"carol": "rejected",
	}

	d, err := r.GetData("decisions")
	if err != nil {
		return fmt.Errorf("read the assembled decisions: %w", err)
	}

	got, ok := d.Value().Get(ctx).(map[string]any)
	if !ok {
		return fmt.Errorf("decisions is %T, want a map", d.Value().Get(ctx))
	}

	if len(got) != len(want) {
		return fmt.Errorf("want %d decisions, got %d: %v",
			len(want), len(got), got)
	}

	for who, decision := range want {
		if got[who] != decision {
			return fmt.Errorf("%s decided %v, want %q", who, got[who], decision)
		}
	}

	fmt.Printf("    decisions:        %v\n\n", got)

	return nil
}
