package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// runProcess registers proc, starts it, and waits for both branches to report
// before asserting the instance completed.
func runProcess(
	engine *thresher.Thresher,
	proc *process.Process,
	done <-chan string,
) error {
	if _, err := engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	h, err := engine.StartLatest(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	// Wait for both branches; the converging gateway then synchronizes them and
	// the surviving token reaches End.
	ran := map[string]bool{}
	for len(ran) < 2 {
		select {
		case name := <-done:
			ran[name] = true

		case <-ctx.Done():
			return fmt.Errorf(
				"timed out waiting for parallel branches (ran: %v)", ran)
		}
	}

	// Both branches ran, but that alone does not prove the join synchronized
	// them: waiting for the instance to reach Completed does. A grace sleep
	// here would pass even if the token never left the gateway.
	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	if state != thresher.StateCompleted {
		return fmt.Errorf("instance ended %s, want Completed", state)
	}

	fmt.Println("✓ parallel-demo completed: split forked both branches, " +
		"join synchronized, one token reached End")

	return nil
}
