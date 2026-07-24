package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print(`
  business-rule-task:
    start → [classify (BRT: decision "discount")]
              ├─ discount_pct > 10 ─> [apply-big-discount] ──> end-big
              └─ (default) ────────> [apply-small-discount] ─> end-small

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	reg, err := buildEngine()
	if err != nil {
		return fmt.Errorf("build rule engine: %w", err)
	}

	engine, err := thresher.New("business-rule-task-engine",
		thresher.WithRuleEngine(reg))
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	proc, err := buildProcess(250)
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

	fmt.Printf("\n✓ business-rule-task completed (%s): the pluggable rule "+
		"engine (##GoRules) evaluated \"discount\" for a 250 order, the task "+
		"committed discount_pct=15, and the conditional flow routed to "+
		"apply-big-discount\n", state)

	return nil
}

// announceTask builds an in-process service task printing msg when executed.
func announceTask(name, msg string) (*activities.ServiceTask, error) {
	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Println(msg)

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("operation %q: %w", name, err)
	}

	st, err := activities.NewServiceTask(name, op, activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("task %q: %w", name, err)
	}

	return st, nil
}
