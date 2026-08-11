package main

import (
	"fmt"
	"slices"

	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// check asserts what the example claims, so a regression fails the run rather
// than merely printing something different (the examples' outcome-gate rule).
func check(state thresher.InstanceState, fired []string) error {
	want := make([]string, 0, len(slaMarks))
	for _, m := range slaMarks {
		want = append(want, m.id)
	}

	// Every mark fired, in ascending-deadline order — separate bounded timers,
	// each landing at its own offset.
	if !slices.Equal(fired, want) {
		return fmt.Errorf("SLA marks fired %v, want %v", fired, want)
	}

	// The point of NON-interrupting: the guarded task survived all three
	// warnings and still completed. An interrupting boundary would have
	// canceled it at the first mark and this would read Terminated.
	if state != thresher.StateCompleted {
		return fmt.Errorf(
			"instance finished %s, want %s — a non-interrupting boundary "+
				"must not cancel the task it guards", state,
			thresher.StateCompleted)
	}

	fmt.Printf("\nprocess finished: %s (SLA marks fired: %v)\n", state, fired)

	return nil
}
