package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/adapters/lua"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// orderLua is the embedded script body — a plain .lua file, editable
// without recompiling the model.
//
//go:embed order.lua
var orderLua string

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print(`
  script-task:
    a Lua script (embedded order.lua) runs on the pluggable adapters/lua
    engine, routed by the task's scriptFormat; its returned table commits
    as named process data.

    start → [classify (ScriptTask: text/x-lua)] → [report] → end

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	// Each case states the lane and discount the Lua script is expected to
	// choose, and runOrder checks the run against it. Without that, an example
	// that classified every order into the wrong lane would still exit 0.
	for _, order := range []struct {
		tier     string
		wantLane string
		total    int
		wantPct  float64
	}{
		{tier: "vip", total: 500, wantLane: "wholesale", wantPct: 25},
		{tier: "retail", total: 150, wantLane: "wholesale", wantPct: 15},
		{tier: "", total: 40, wantLane: "retail", wantPct: 5},
	} {
		fmt.Printf("\norder: tier=%q total=%d\n", order.tier, order.total)

		if err := runOrder(
			order.total, order.tier, order.wantLane, order.wantPct); err != nil {
			return err
		}
	}

	fmt.Print("\n✓ script-task completed: three orders classified by the " +
		"sandboxed Lua script (25/15/5%)\n")

	return nil
}

// runOrder runs one process instance for an order profile and checks that it
// both COMPLETED and classified the order as claimed.
func runOrder(total int, tier, wantLane string, wantPct float64) error {
	engine, err := thresher.New(
		fmt.Sprintf("script-task-%d", total),
		thresher.WithoutBanner(),
		thresher.WithoutStartupConfig(),
		thresher.WithScriptEngine(lua.New()))
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	proc, err := buildProcess(total, tier)
	if err != nil {
		return fmt.Errorf("build process: %w", err)
	}

	if _, err := engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	h, err := engine.StartLatest(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	if state != thresher.StateCompleted {
		return fmt.Errorf("process finished %s, want %s",
			state, thresher.StateCompleted)
	}

	// Read what the script actually decided back out of the finished instance.
	// data.As, not a bare .(T) assertion: Lua numbers arrive as float64, and a
	// silent mismatch would record a zero and blame the script for it.
	dr := h.Data()

	laneD, err := dr.GetData("lane")
	if err != nil {
		return fmt.Errorf("read lane: %w", err)
	}

	pctD, err := dr.GetData("discount_pct")
	if err != nil {
		return fmt.Errorf("read discount_pct: %w", err)
	}

	lane, err := data.As[string](ctx, laneD.Value())
	if err != nil {
		return fmt.Errorf("lane: %w", err)
	}

	pct, err := data.As[float64](ctx, pctD.Value())
	if err != nil {
		return fmt.Errorf("discount_pct: %w", err)
	}

	if lane != wantLane || pct != wantPct {
		return fmt.Errorf(
			"order tier=%q total=%d classified lane=%q discount=%v%%, "+
				"want lane=%q discount=%v%%",
			tier, total, lane, pct, wantLane, wantPct)
	}

	return nil
}
