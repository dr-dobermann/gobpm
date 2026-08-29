// Command multi-instance-human demonstrates a PARALLEL Multi-Instance over a
// User Task: three approvals offered at once, each its own addressable task,
// completed by its own reviewer.
//
// It shows the three things that make the construct usable rather than merely
// possible — an identity per iteration, a declared result keyed per iteration,
// and an account of who did which one — and it runs unattended: the inbox here
// answers each task as the reviewer it was offered to.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print(`
  multi-instance-human:
    start → approve [Multi-Instance over reviewers, parallel] → report → end
            │  three tasks announced AT ONCE, one per reviewer
            │  each claimed and completed by that reviewer alone
            │  result map keyed per instance: reviewer → decision
            └─ report reads RUNTIME/ITERATION_OWNERS and RUNTIME/ITERATIONS

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	inbox := newInbox()

	engine, err := thresher.New("multi-instance-human-engine",
		thresher.WithTaskDistributor(inbox),
		thresher.WithoutBanner(), thresher.WithoutStartupConfig())
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	inbox.bind(engine)

	if err = engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	proc, err := buildProcess()
	if err != nil {
		return fmt.Errorf("build process: %w", err)
	}

	if _, err = engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	h, err := engine.StartLatest(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	if _, err := h.WaitCompletion(ctx); err != nil {
		return fmt.Errorf("wait completion: %w", err)
	}

	if err := checkRun(ctx, inbox, h.Data()); err != nil {
		return err
	}

	return engine.Shutdown(context.Background())
}
