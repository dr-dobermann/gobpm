package main

import (
	"fmt"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// check asserts what the example claims, so a regression fails the run rather
// than merely printing something different (the examples' outcome-gate rule).
func check(state thresher.InstanceState, view thresher.IncidentView) error {
	if state != thresher.StateCompleted {
		return fmt.Errorf("instance finished %s, want %s",
			state, thresher.StateCompleted)
	}

	if got := attempts.Load(); got != healAt {
		return fmt.Errorf("charge ran %d times, want %d "+
			"(fail, policy retry fails, operator retry succeeds)",
			got, healAt)
	}

	if view.State != "resolved" {
		return fmt.Errorf("incident closed %q, want resolved", view.State)
	}

	if !strings.Contains(string(view.Data), "order_id") {
		return fmt.Errorf(
			"the failure-time snapshot must carry the order_id property")
	}

	fmt.Println()
	fmt.Println("OK: the failure never killed the instance — one incident,")
	fmt.Println("    two failed attempts on record, resolved by the operator.")

	return nil
}
