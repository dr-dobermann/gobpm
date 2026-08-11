package main

import (
	"context"
	"fmt"
	"time"

	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// runProcess registers the model, starts it, waits for both branches, then
// reads each result back from the instance's own scope.
func runProcess(engine *thresher.Thresher, m *demo, done <-chan string) error {
	if _, err := engine.RegisterProcess(m.proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	h, err := engine.StartLatest(m.proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	ran := map[string]bool{}
	for len(ran) < 2 {
		select {
		case name := <-done:
			ran[name] = true

		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for branches (ran: %v)", ran)
		}
	}

	// Brief grace for the producer stages: outputs flow to the DataObjects
	// and the frames commit.
	time.Sleep(200 * time.Millisecond)

	bg := context.Background()

	for _, res := range []struct {
		do   *dataobjects.DataObject
		want string
	}{
		{m.resultA, "Hello, dr.Dobermann!"},
		{m.resultB, "Welcome, dr.Dobermann!"},
	} {
		// Read the result from THIS instance's scope, by the DataObject's name,
		// through the instance handle's data reader (SRD-063).
		datum, readErr := h.Data().GetData(res.do.Name())
		if readErr != nil {
			return fmt.Errorf("read data object %q: %w", res.do.Name(), readErr)
		}

		got, ok := datum.Value().Get(bg).(string)
		if !ok || got != res.want {
			return fmt.Errorf("data object %q: want %q, got %v",
				res.do.Name(), res.want, got)
		}

		fmt.Printf("  ✓ %s = %q\n", res.do.Name(), got)
	}

	fmt.Println("✓ data-demo completed: the property fed both branches " +
		"through their frames; each result reached its per-instance DataObject " +
		"in scope, read back by name")

	return nil
}
