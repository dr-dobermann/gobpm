package main

import (
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// check asserts the run did what the example claims. The point is the ownership
// flow: the task is announced to the distributor, the driver takes exclusive
// hold, and only then completes. All three are asserted because a driver that
// quietly skipped the claim would look identical from the outside.
func check(state thresher.InstanceState, watch *lifecycleWatch) error {
	if state != thresher.StateCompleted {
		return fmt.Errorf("process finished %s, want %s",
			state, thresher.StateCompleted)
	}

	if absent := watch.missing(
		2*time.Second,
		observability.PhaseAnnounced,
		observability.PhaseClaimed,
		observability.PhaseCompleted,
	); len(absent) > 0 {
		return fmt.Errorf("user task never reached %v", absent)
	}

	return nil
}
