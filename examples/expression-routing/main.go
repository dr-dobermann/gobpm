// Command expression-routing demonstrates the language-routed expression
// layer (ADR-032) at three consumer sites in one process: mixed lite +
// goexpr conditions on a task's outgoing flows, a lite time() branch on
// an exclusive gateway, and a UserTask whose assignee is computed by a
// lite string expression — all on the zero-config batteries registry.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/interactor/console"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// manager is the acting human: the UserID matches what the lite
// assignee expression computes ('order.customer.tier + "-manager"').
type manager struct{}

func (manager) UserID() string   { return "vip-manager" }
func (manager) Groups() []string { return nil }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Print(`
  expression-routing (one process, two engines, three sites):
    task flows:  lite "order.total > 100 and ..." beside a Go functor
    gateway:     lite "deadline < time(...)" vs the default flow
    user task:   assignee = lite 'order.customer.tier + "-manager"'

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	proc, err := buildProcess()
	if err != nil {
		return fmt.Errorf("build process: %w", err)
	}

	driver := console.New(manager{}, os.Stdout)

	engine, err := thresher.New("expression-routing",
		thresher.WithoutBanner(),
		thresher.WithoutStartupConfig(),
		thresher.WithTaskDistributor(driver))
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	driver.Bind(engine)

	ctx, cancel := context.WithTimeout(context.Background(),
		15*time.Second)
	defer cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	if _, err := engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	h, err := engine.StartLatest(proc.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	if _, err := h.WaitCompletion(ctx); err != nil {
		return fmt.Errorf("waiting for completion: %w", err)
	}

	fmt.Print("\n✓ expression-routing completed: both engines routed " +
		"their own languages,\n  and the lite-computed assignee " +
		"approved the urgent order\n")

	return nil
}
