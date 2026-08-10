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

	// newTotal is the value written through the view; it is above the
	// gateway's threshold, so it also decides the lane.
	const newTotal = 150

	// The host's live object — note Secret is tagged gobpm:"-".
	order := &Order{ID: "A-1", Total: 90,
		Items:  []Item{{SKU: "widget", Price: 50}},
		Secret: "host-only"}

	wrapped := adapters.MustWrap(order)

	// A host-side structural write goes through the view INTO the live struct.
	if err := values.SetPath(context.Background(), wrapped,
		"total", values.NewVariable(newTotal)); err != nil {
		return fmt.Errorf("set order.total: %w", err)
	}

	// The whole point of wrapping a native struct is that the write reaches
	// the LIVE object, not a copy: if it did not, everything downstream would
	// still behave — the view would hold 150 and the gateway would route on
	// it — while the host's own struct silently kept 90.
	if order.Total != newTotal {
		return fmt.Errorf("SetPath left the live struct at %d, want %d",
			order.Total, newTotal)
	}

	fmt.Printf("  SetPath(order.total=%d) → the LIVE struct: o.Total == %d\n",
		newTotal, order.Total)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	engine, err := thresher.New("native-structs-engine",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig())
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	changes := &dataChangePrinter{}
	sub := engine.Observe(changes)
	defer sub.Cancel()

	if err = engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	// Records which lane ran, so the structural condition is checked.
	ran := newPathSet()

	proc, err := buildProcess(ran, wrapped)
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

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("wait completion: %w", err)
	}

	sub.Cancel() // drain the buffered facts before the final line

	// A native Go struct wrapped as process data diffs by FIELD PATH just as
	// a record does: one Value_Added at the root when it is first committed,
	// one Value_Updated at receipt.sum when only that field changes. Nothing
	// about the wrapping should make the change stream coarser.
	// The gateway condition reaches into the wrapped struct, so which lane
	// ran proves the reach-in worked end to end.
	if err := ran.check(
		[]string{"premium"}, []string{"standard"},
	); err != nil {
		return fmt.Errorf("structural routing: %w", err)
	}

	if err := changes.check(
		"Value_Added receipt @quote",
		"Value_Updated receipt.sum @reprice",
	); err != nil {
		return fmt.Errorf("change stream: %w", err)
	}

	fmt.Printf("  ✓ completed (%s)\n", state)

	return nil
}
