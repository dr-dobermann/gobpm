package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/adapters"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// mapDemos shows the two map tiers navigating identically: the dynamic
// values.Map (engine-assembled) and a wrapped native map[string]V (the host's
// own map, live). Both enumerate in sorted key order and address by ["key"].
func mapDemos() error {
	ctx := context.Background()

	// Dynamic tier: assemble a dictionary, grow it key-by-key, read a ["key"]
	// path. Keys enumerate in sorted order regardless of insertion order.
	fx := values.MustMap(map[string]float64{"USD": 1.0, "EUR": 1.08})
	if err := fx.SetEntry(ctx, "GBP", 1.27); err != nil {
		return fmt.Errorf("set GBP: %w", err)
	}

	fmt.Printf("  dynamic values.Map keys (sorted): %v\n", fx.Keys())

	eur, err := data.ResolvePath(ctx, `fx["EUR"]`,
		func(string) (data.Data, error) {
			return data.NewPathData("fx", fx)
		})
	if err != nil {
		return fmt.Errorf("resolve fx[\"EUR\"]: %w", err)
	}

	fmt.Printf("  read fx[\"EUR\"] via the [\"key\"] step: %v\n",
		eur.Value().Get(ctx))

	// Native tier: the host's OWN map participates directly (wrap, not
	// convert) — SetEntry writes through into the live map.
	limits := map[string]int{"day": 100}

	w, err := adapters.Wrap(&limits)
	if err != nil {
		return fmt.Errorf("wrap native map: %w", err)
	}

	if err := w.(data.Map).SetEntry(ctx, "week", 500); err != nil {
		return fmt.Errorf("set week: %w", err)
	}

	fmt.Printf("  native map[string]int, live after SetEntry: %v\n\n", limits)

	return nil
}
