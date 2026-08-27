package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// report checks the run against the claim the example makes and prints the
// verdict.
func report(state thresher.InstanceState, runLog *runLog) error {
	// Two orderings are being demonstrated at once: compensation undoes the
	// completed activities in REVERSE (refund before release, mirroring
	// reserve then charge), and only afterwards does control leave through
	// the Cancel boundary to notify-customer. A run that notified first, or
	// undid forwards, would execute every task and complete just the same.
	if err := runLog.check(
		"reserve-seat", "charge-card",
		"refund-card", "release-seat",
		"notify-customer",
	); err != nil {
		return fmt.Errorf("cancel ordering: %w", err)
	}

	fmt.Printf("\n✓ transaction-sub-process completed (%s): the Cancel End "+
		"compensated the booking in reverse order and control left through the "+
		"Cancel boundary\n", state)

	return nil
}
