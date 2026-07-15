package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// condEventPrinter surfaces the conditional subscription's lifecycle: the
// EventFlow facts of the watch node (Registered on arm, Fired on the
// false→true edge) — the observable trace of data-driven waiting.
type condEventPrinter struct{}

// OnFact prints one line per EventFlow fact of the watch node.
func (p *condEventPrinter) OnFact(f observability.Fact) {
	if f.Kind != observability.KindEventFlow || f.NodeName != "watch-total" {
		return
	}

	fmt.Printf("  ▶ watch-total: %s\n", f.Phase)
}
