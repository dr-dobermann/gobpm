package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// runProcess registers proc, starts it and checks the timer actually held the
// token — each step's error in its own short scope.
func runProcess(engine *thresher.Thresher, proc *process.Process) error {
	if _, err := engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	started := time.Now()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	fmt.Printf("Timer process started. Will fire in %s...\n", timerDelay)

	// The timer start event instantiates the process on its own — there is no
	// StartProcess call and so no handle to wait on, so the instance is found
	// through the engine. Waiting for it is the whole assertion: this example
	// used to sleep past the deadline and print "Timer should have fired by
	// now!", which was true whether or not it had.
	h, err := engine.StartLatest(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	if err := awaitHeldByTimer(ctx, h, started); err != nil {
		return err
	}

	fmt.Println("Timer fired: the token was released and the process completed")
	return nil
}
