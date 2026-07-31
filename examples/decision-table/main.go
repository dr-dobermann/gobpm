package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/adapters/dtable"
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
  decision-table:
    a JSON decision table (embedded artifact) deployed onto the pluggable
    adapters/dtable engine; the Business Rule Task evaluates it per order.

    start → [classify (BRT: "discount")] → [report] → end

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	ruleEngine, err := buildEngine()
	if err != nil {
		return err
	}

	// wantPct is the rate the deployed FIRST-policy table must choose for each
	// profile — the same claim this example prints when it finishes. Checking it
	// is what separates "the table was consulted" from "the table was right":
	// a table that returned the fallthrough rate for every order would otherwise
	// complete three times and exit 0.
	for _, order := range []struct {
		tier    string
		total   int
		wantPct float64
	}{
		{tier: "vip", total: 500, wantPct: 25},
		{tier: "retail", total: 150, wantPct: 15},
		{tier: "retail", total: 40, wantPct: 5},
	} {
		fmt.Printf("\norder: tier=%s total=%d\n", order.tier, order.total)

		if err := runOrder(
			ruleEngine, order.total, order.tier, order.wantPct); err != nil {
			return err
		}
	}

	fmt.Print("\n✓ decision-table completed: three orders classified by the " +
		"deployed FIRST-policy table (vip+big 25%, big 15%, default 5%)\n")

	return nil
}

// runOrder runs one process instance for an order profile against the
// shared, already-deployed rule engine.
func runOrder(
	ruleEngine *dtable.Engine, total int, tier string, wantPct float64,
) error {
	engine, err := thresher.New(
		fmt.Sprintf("decision-table-%s-%d", tier, total),
		thresher.WithoutBanner(),
		thresher.WithoutStartupConfig(),
		thresher.WithRuleEngine(ruleEngine))
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
		return fmt.Errorf("order tier=%s total=%d finished %s, want %s",
			tier, total, state, thresher.StateCompleted)
	}

	pctD, err := h.Data().GetData("discount_pct")
	if err != nil {
		return fmt.Errorf("read discount_pct: %w", err)
	}

	// data.As rather than a bare .(T): the yields commit float64, and a silent
	// mismatch would compare a zero against the expected rate.
	pct, err := data.As[float64](ctx, pctD.Value())
	if err != nil {
		return fmt.Errorf("discount_pct: %w", err)
	}

	if pct != wantPct {
		return fmt.Errorf("order tier=%s total=%d got %v%%, want %v%%",
			tier, total, pct, wantPct)
	}

	return nil
}
