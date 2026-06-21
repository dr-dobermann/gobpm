// Command signal-broadcast demonstrates BPMN signal broadcast: one thrown
// signal is caught by EVERY waiting catcher, across independent instances.
//
// Two instances of a "watcher" process each park on an intermediate catch of
// the signal "order-cancelled"; a single "canceller" instance throws it once,
// and BOTH watchers resume — a signal has no correlation, it broadcasts to all
// catchers in reach (ADR-006 §2.1).
//
// The process build lives in process.go; this file is the engine wiring + run.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print(`
  signal-broadcast (one throw → every catcher):
    watcher ×2:  start → catch(order-cancelled) → end
    canceller:   start → throw(order-cancelled) → end

`)
	const signal = "order-cancelled"

	engine, err := thresher.New("signal-broadcast-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	catcher, err := catcherProcess("watcher", signal)
	if err != nil {
		return fmt.Errorf("build catcher: %w", err)
	}

	thrower, err := throwerProcess("canceller", signal)
	if err != nil {
		return fmt.Errorf("build thrower: %w", err)
	}

	if err := engine.RegisterProcess(catcher); err != nil {
		return fmt.Errorf("register catcher: %w", err)
	}

	if err := engine.RegisterProcess(thrower); err != nil {
		return fmt.Errorf("register thrower: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	// Two independent watcher instances, both waiting on the one signal.
	h1, err := engine.StartProcess(catcher.ID())
	if err != nil {
		return fmt.Errorf("start watcher 1: %w", err)
	}

	h2, err := engine.StartProcess(catcher.ID())
	if err != nil {
		return fmt.Errorf("start watcher 2: %w", err)
	}

	time.Sleep(200 * time.Millisecond) // both reach and park on the catch
	fmt.Println("  ▶ two watcher instances are waiting on \"order-cancelled\"")

	if _, err := engine.StartProcess(thrower.ID()); err != nil { // one broadcast
		return fmt.Errorf("start canceller: %w", err)
	}
	fmt.Println("  ▶ one canceller threw the signal once")

	for i, h := range []*thresher.InstanceHandle{h1, h2} {
		st, err := h.WaitCompletion(ctx)
		if err != nil {
			return fmt.Errorf("watcher %d completion: %w", i+1, err)
		}
		fmt.Printf("  ✓ watcher %d completed (%s) — caught the broadcast\n", i+1, st)
	}

	fmt.Println("✓ one throw → every waiting instance caught it (broadcast)")

	return nil
}
