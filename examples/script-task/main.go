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

	for _, order := range []struct {
		tier  string
		total int
	}{
		{tier: "vip", total: 500},
		{tier: "retail", total: 150},
		{tier: "", total: 40},
	} {
		fmt.Printf("\norder: tier=%q total=%d\n", order.tier, order.total)

		if err := runOrder(order.total, order.tier); err != nil {
			return err
		}
	}

	fmt.Print("\n✓ script-task completed: three orders classified by the " +
		"sandboxed Lua script (25/15/5%)\n")

	return nil
}

// runOrder runs one process instance for an order profile.
func runOrder(total int, tier string) error {
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

	if _, err := h.WaitCompletion(ctx); err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	return nil
}
