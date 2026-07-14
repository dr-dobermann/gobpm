package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/dr-dobermann/gobpm/pkg/tasks/localdispatcher"
)

// quoteWorker is the pricing handler: it returns a FLAT receipt body —
// {total, price0, price1} — with no nested shape at all. The nesting is the
// task's job: its output mapping assembles these flat fields into one structured
// «order» record via structural Var paths (ADR-011 v.6 §2.9.3, §2.9.5).
func quoteWorker() localdispatcher.WorkerFunc {
	return func(_ context.Context, _ tasks.LockedJob) (*data.ItemDefinition, error) {
		fmt.Println("  quote worker → flat body {total:150, price0:50, price1:100}")

		body := values.MustRecord(
			values.F("total", values.NewVariable(150)),
			values.F("price0", values.NewVariable(50)),
			values.F("price1", values.NewVariable(100)),
		)

		return data.MustItemDefinition(body, foundation.WithID("body")), nil
	}
}
