// Command maps demonstrates the map value kind (ADR-011 v.7 §2.9.7, SRD-047):
// a data-keyed dictionary beside record and list. It shows the dynamic
// values.Map and a wrapped native map[string]V navigating by the ["key"] path
// step, then runs a process whose committed rates map surfaces per-entry
// DataChange facts (rates["EUR"]). See README.md.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print("\n  maps: a data-keyed dictionary, navigable by [\"key\"]\n\n")

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	if err := mapDemos(); err != nil {
		return fmt.Errorf("map demos: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	engine, err := thresher.New("maps-engine",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig())
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	changes := &dataChangePrinter{}
	sub := engine.Observe(changes)
	defer sub.Cancel()

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	proc, err := buildProcess()
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

	// A map commit diffs PER KEY: the second commit updates EUR, adds JPY and
	// deletes GBP, and each must surface as its own change at its own key
	// path. A diff that re-reported the whole map, or missed the deletion,
	// would complete the process exactly the same way.
	//
	// Checked as a set, not a sequence: the three second-commit changes are
	// derived from a map, whose iteration order Go deliberately leaves
	// unspecified, so asserting an order would be asserting a coincidence.
	if err := changes.checkSet(
		"Value_Added rates @publish",
		`Value_Updated rates["EUR"] @reprice`,
		`Value_Added rates["JPY"] @reprice`,
		`Value_Deleted rates["GBP"] @reprice`,
	); err != nil {
		return fmt.Errorf("change stream: %w", err)
	}

	fmt.Printf("  ✓ completed (%s)\n", state)

	return nil
}
