package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// scopePrinter surfaces the nested-scope lifecycle: Opened when the host
// token enters the fragment, Completed when its scope drains.
type scopePrinter struct{}

// OnFact prints one line per Scope fact.
func (p *scopePrinter) OnFact(f observability.Fact) {
	if f.Kind != observability.KindScope {
		return
	}

	fmt.Printf("  ▶ scope %s: %s (%s)\n",
		f.NodeName, f.Phase, f.Details[observability.AttrScopePath])
}
