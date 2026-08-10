// Command usertask-sla demonstrates SLA monitoring on a UserTask with three
// bounded, non-interrupting timer boundaries at 50%, 90% and 100% of the
// approval's budget. The approval deliberately overruns, so all three fire —
// and the task still completes, because non-interrupting warnings do not
// cancel the work they warn about.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// overrun is how long the operator holds the task: past the 100% mark, so every
// SLA warning fires before the approval lands.
const overrun = sla + sla/5

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	banner()

	rec := &notices{}

	p, err := buildProcess(rec)
	if err != nil {
		return err
	}

	driver := &slowOperator{hold: overrun}

	th, err := thresher.New("sla-engine",
		thresher.WithTaskDistributor(driver))
	if err != nil {
		return err
	}

	driver.Bind(th)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	state, err := runProcess(ctx, th, p)
	if err != nil {
		return err
	}

	return check(state, rec.fired())
}
