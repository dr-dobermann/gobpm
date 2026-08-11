package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// runProcess registers proc, starts it and checks that the timer actually held
// the token for timerDelay before the instance completed.
//
// It is a function rather than a stretch of run() so each step's error lives in
// one short scope: a single err threaded through the whole run makes every
// scoped `if err := …` after it a shadow.
func runProcess(engine *thresher.Thresher, proc *process.Process) error {
	if _, err := engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	started := time.Now()

	h, err := engine.StartLatest(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	fmt.Printf("Timer process %q started with ID: %s\n", proc.Name(), proc.ID())
	fmt.Println("Timer will trigger after 5 seconds...")

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	if state != thresher.StateCompleted {
		return fmt.Errorf("instance ended %s, want Completed", state)
	}

	// The delay is the demonstration, so it is what gets checked: a timer that
	// fired immediately — or a start event that ignored its trigger altogether
	// — would complete the process just the same, only sooner. The earlier
	// version of this example simply waited for the context to expire and then
	// declared success, which no failure could have upset.
	if waited := time.Since(started); waited < timerDelay {
		return fmt.Errorf("completed after %s, want at least %s — the timer "+
			"did not hold the token",
			waited.Round(time.Millisecond), timerDelay)
	}

	fmt.Println("Process completed")

	return nil
}
