// Command simple-timer demonstrates a timer Start Event: the instance is
// started explicitly, and its timer start event holds the token until the
// timer fires (here, 3 seconds) before releasing it into the flow.
//
//	(timer start — fires in 3s) ◷─> end
//
// A timer start does NOT instantiate the process by schedule — the engine
// treats only message, signal and instantiating-ReceiveTask starts as
// instantiating triggers (see internal/instance/snapshot). Scheduled
// instantiation is not implemented; this example would silently demonstrate
// nothing if it relied on it.
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// timerDelay is how long the start event's timer waits before instantiating
// the process. The timer definition and the example's own assertion both read
// it, so the check can never drift away from the behavior it is checking.
const timerDelay = 3 * time.Second

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print(`
  simple-timer:
    (timer start — fires in 3s) ◷─> end

`)

	// Engine-level extensions (logger, repository, clock, metrics, message
	// broker, expression engine, authorization, worker dispatcher) are
	// configurable via functional options; any option you omit falls back to a
	// bundled default (slog.Default(), an in-memory repository, the system
	// clock). Here we override just the logger.
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	engine, err := thresher.New("simple-timer-engine",
		thresher.WithLogger(logger),
	)
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	proc, err := buildProcess()
	if err != nil {
		return err
	}

	return runProcess(engine, proc)
}

// awaitHeldByTimer waits for the instance to complete and checks that it did
// not finish sooner than the timer allows — the delay is the demonstration, so
// it is what gets checked.
func awaitHeldByTimer(
	ctx context.Context, h *thresher.InstanceHandle, started time.Time,
) error {
	st, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	if st != thresher.StateCompleted {
		return fmt.Errorf("instance ended %s, want Completed", st)
	}

	if waited := time.Since(started); waited < timerDelay {
		return fmt.Errorf("completed after %s, want at least %s — the timer "+
			"start did not hold the token",
			waited.Round(time.Millisecond), timerDelay)
	}

	return nil
}
