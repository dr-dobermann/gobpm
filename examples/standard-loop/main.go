// Command standard-loop demonstrates a BPMN Standard Loop (§13.3.6, SRD-054):
// an activity marked WithLoop re-runs while its loopCondition holds. Here a
// single Service Task runs three times, reading the engine-published 0-based
// loopCounter each pass (the condition is loopCounter < 3). See README.md.
package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// passes is the loopCounter sequence a post-tested loop must produce.
func passes() []string {
	p := make([]string, wantPasses)
	for i := range p {
		p[i] = strconv.Itoa(i)
	}

	return p
}

func run() error {
	fmt.Print(`
  standard-loop:
    start → work [loopCounter < 3] → end
            (a post-tested loop: runs at loopCounter 0, 1, 2)

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	engine, err := thresher.New("standard-loop-engine",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig())
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	if err = engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	// Records the counter seen on each pass, so the loop claim is checked.
	runLog := newRunLog()

	proc, err := buildProcess(runLog)
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

	// A post-tested loop runs the body first and tests after, so it must make
	// exactly wantPasses passes, seeing 0, 1, 2 — one short would mean it was
	// pre-tested, one long that the condition was read a pass late.
	if err := runLog.check(passes()...); err != nil {
		return fmt.Errorf("loop passes: %w", err)
	}

	fmt.Println("  process completed — the loop ran to its condition.")

	return engine.Shutdown(context.Background())
}
