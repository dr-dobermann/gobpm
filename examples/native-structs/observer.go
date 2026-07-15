package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// dataChangePrinter surfaces the DataChange facts — the per-path change
// signal the commit-diff produces over the WRAPPED receipts (observer-only,
// never echoed to the operator log).
type dataChangePrinter struct{}

// OnFact prints one line per DataChange fact: phase, path, committing node.
func (p *dataChangePrinter) OnFact(f observability.Fact) {
	if f.Kind != observability.KindDataChange {
		return
	}

	fmt.Printf("  ▶ %s %s @%s\n",
		f.Phase, f.Details[observability.AttrDataPath], f.NodeName)
}
