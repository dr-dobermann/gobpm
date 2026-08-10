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
  multi-instance-behavior:
    start → board [parallel Multi-Instance over reviewers] → end
            each reviewer votes concurrently; a Complex behavior throws a
            "quorum-reached" signal once 2 votes are in, caught by a
            non-interrupting boundary that posts a notification.

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	engine, err := thresher.New("multi-instance-behavior-engine",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig())
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	if err = engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	// Records votes and the notification in order, so the quorum claim holds.
	log := newRunLog()

	proc, err := buildProcess(log)
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

	// Every reviewer must vote, and the notification must not fire before the
	// quorum is actually met — a behavior that threw on the first completion
	// would print the same line and finish the same way. It fires once per
	// completion at or past the threshold, so the count is not fixed; what is
	// checkable is that it never fires early.
	if got := log.count("vote"); got != len(reviewers) {
		return fmt.Errorf("%d reviewers voted, want %d", got, len(reviewers))
	}

	if err := log.precede("notify", "vote", quorumSize); err != nil {
		return fmt.Errorf("quorum: %w", err)
	}

	fmt.Print("\n  completed — the board finished; the quorum notification " +
		"fired as votes crossed the threshold.\n")

	return engine.Shutdown(context.Background())
}
