// Command native-structs demonstrates native-struct adapters (ADR-011 v.6
// §2.9.5, SRD-045): the host's OWN Order struct participates directly as
// process data — adapters.Wrap returns a live view (wrap, not convert), a
// host-side SetPath writes through it into the live struct, the gateway
// routes on order.total reaching into it, and committed wrapped receipts
// surface per-path DataChange facts. See README.md.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/adapters"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print(`
  native-structs:
    the host's own Order struct IS the process data (wrap, not convert)

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	// The host's live object — note Secret is tagged gobpm:"-".
	order := &Order{ID: "A-1", Total: 90,
		Items:  []Item{{SKU: "widget", Price: 50}},
		Secret: "host-only"}

	wrapped := adapters.MustWrap(order)

	// A host-side structural write goes through the view INTO the live struct.
	if err := values.SetPath(context.Background(), wrapped,
		"total", values.NewVariable(150)); err != nil {
		return fmt.Errorf("set order.total: %w", err)
	}

	fmt.Printf("  SetPath(order.total=150) → the LIVE struct: o.Total == %d\n",
		order.Total)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	engine, err := thresher.New("native-structs-engine",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig())
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	sub := engine.Observe(&dataChangePrinter{})
	defer sub.Cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	proc, err := buildProcess(wrapped)
	if err != nil {
		return fmt.Errorf("build process: %w", err)
	}

	if _, err := engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	h, err := engine.StartLatest(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("wait completion: %w", err)
	}

	sub.Cancel() // drain the buffered facts before the final line

	fmt.Printf("  ✓ completed (%s)\n", state)

	return nil
}
