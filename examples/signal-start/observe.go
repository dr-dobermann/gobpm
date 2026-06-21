package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// awaitAll waits for n signal-born instances to appear and complete. The
// instances are born from the broadcast (not StartProcess), so they're
// discovered via engine.Instances rather than from a StartProcess handle.
func awaitAll(ctx context.Context, engine *thresher.Thresher, n int) error {
	deadline := time.After(8 * time.Second)
	done := map[string]bool{}

	for len(done) < n {
		for _, id := range engine.Instances(thresher.InstancesAll) {
			if done[id] {
				continue
			}

			h, ok := engine.Instance(id)
			if !ok {
				continue
			}

			st, err := h.WaitCompletion(ctx)
			if err != nil {
				return fmt.Errorf("instance %s: %w", id, err)
			}

			fmt.Printf("  ✓ instance %s completed (%s)\n", id, st)
			done[id] = true
		}

		select {
		case <-deadline:
			return fmt.Errorf("expected %d instances, saw %d", n, len(done))
		case <-time.After(20 * time.Millisecond):
		}
	}

	return nil
}
