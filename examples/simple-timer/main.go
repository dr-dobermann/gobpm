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

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// timerDelay is how long the start event's timer waits before instantiating
// the process. The timer definition and the example's own assertion both read
// it, so the check can never drift away from the behaviour it is checking.
const timerDelay = 3 * time.Second

func main() {
	fmt.Print(`
  simple-timer:
    (timer start — fires in 3s) ◷─> end

`)
	// Create the BPM engine. Engine-level extensions (logger, repository,
	// clock, metrics, message broker, expression engine, authorization,
	// worker dispatcher) are configurable via functional options; any option
	// you omit falls back to a sensible bundled default (e.g. slog.Default(),
	// an in-memory repository, the system clock). Here we override just the
	// logger — the rest stay on their defaults.
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	engine, err := thresher.New("simple-timer-engine",
		thresher.WithLogger(logger),
	)
	if err != nil {
		log.Fatal("Failed to create BPM engine:", err)
	}

	// Create process
	proc, err := process.New("simple-timer")
	if err != nil {
		log.Fatal("Failed to create process:", err)
	}

	// Create timer expression for 3 seconds from now
	timeExpr := goexpr.Must(
		nil,
		data.MustItemDefinition(values.NewVariable(time.Now().Add(timerDelay))),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			return values.NewVariable(time.Now().Add(timerDelay)), nil
		},
		foundation.WithID("timer-3s"),
	)

	// Create timer event definition
	timerDef, err := events.NewTimerEventDefinition(timeExpr, nil, nil)
	if err != nil {
		log.Fatal("Failed to create timer definition:", err)
	}

	// Create timer start event
	timerStart, err := events.NewStartEvent("timer-start",
		events.WithTimerTrigger(timerDef))
	if err != nil {
		log.Fatal("Failed to create timer start event:", err)
	}

	// Create end event
	endEvent, err := events.NewEndEvent("end")
	if err != nil {
		log.Fatal("Failed to create end event:", err)
	}

	// Add elements to process
	if err := proc.Add(timerStart); err != nil {
		log.Fatal("Failed to add timer start to process:", err)
	}
	if err := proc.Add(endEvent); err != nil {
		log.Fatal("Failed to add end event to process:", err)
	}

	// Link timer start to end (simple process)
	_, err = flow.Link(timerStart, endEvent)
	if err != nil {
		log.Fatal("Failed to link elements:", err)
	}

	// Register and start engine
	if _, err := engine.RegisterProcess(proc); err != nil {
		log.Fatal("Failed to register process:", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	started := time.Now()

	if err := engine.Run(ctx); err != nil {
		log.Fatal("Failed to start engine:", err)
	}

	fmt.Printf("Timer process started. Will fire in %s...\n", timerDelay)

	// The timer start event instantiates the process on its own — there is no
	// StartProcess call and so no handle to wait on, so the instance is found
	// through the engine. Waiting for it is the whole assertion: this example
	// used to sleep past the deadline and print "Timer should have fired by
	// now!", which was true whether or not it had.
	h, err := engine.StartLatest(proc.ID())
	if err != nil {
		log.Fatal("Failed to start process:", err)
	}

	if err := awaitHeldByTimer(ctx, h, started); err != nil {
		log.Fatal(err)
	}

	fmt.Println("Timer fired: the token was released and the process completed")
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
