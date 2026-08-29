package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// reportOperation reads the two engine-published registers a later node can
// ask about an activity that has already finished.
//
// `RUNTIME/ITERATIONS` says what the iteration DID — the shape, the total, and
// how the instances ended. `RUNTIME/ITERATION_OWNERS` says WHO did which one,
// keyed by ordinal. `RUNTIME/COMPLETED_BY` cannot answer the second: it keys by
// node, so an iterated activity has a single entry however many instances ran.
//
// Both are keyed by activity id, which is what keeps the runtime name set
// closed however many activities iterate.
func reportOperation() (service.Operation, error) {
	return gooper.New("report",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			iters, err := r.GetData("RUNTIME/ITERATIONS")
			if err != nil {
				return nil, fmt.Errorf("read RUNTIME/ITERATIONS: %w", err)
			}

			owners, err := r.GetData("RUNTIME/ITERATION_OWNERS")
			if err != nil {
				return nil, fmt.Errorf("read RUNTIME/ITERATION_OWNERS: %w", err)
			}

			fmt.Printf("\n  the activity, seen from a later node:\n")
			fmt.Printf("    ITERATIONS:       %v\n", iters.Value().Get(ctx))
			fmt.Printf("    ITERATION_OWNERS: %v\n", owners.Value().Get(ctx))

			return data.MustItemDefinition(
				values.NewVariable("reported"),
				foundation.WithID("report-result")), nil
		})
}
