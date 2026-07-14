package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// dataChangePrinter is an engine-scope observer that surfaces only the
// DataChange facts — the per-path change signal the commit-diff produces.
// DataChange is observer-only (never echoed to the operator log), so an
// observer like this is the way to see it.
type dataChangePrinter struct{}

// OnFact prints one line per DataChange fact: the change kind (the phase),
// the changed data path, and the node whose commit produced it.
func (p *dataChangePrinter) OnFact(f observability.Fact) {
	if f.Kind != observability.KindDataChange {
		return
	}

	fmt.Printf("  ▶ %s %s @%s\n",
		f.Phase, f.Details[observability.AttrDataPath], f.NodeName)
}
