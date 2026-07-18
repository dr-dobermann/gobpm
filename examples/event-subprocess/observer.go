package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// scopePrinter narrates the two facts this example is about: the scope
// lifecycle (open / canceled / completed) and the Event Sub-Process handler
// lifecycle (armed / fired / disarmed — a Boundary-kind fact carrying a scope
// path, which is how a handler is told apart from a plain boundary event).
type scopePrinter struct{}

func (p *scopePrinter) OnFact(f observability.Fact) {
	switch f.Kind {
	case observability.KindScope:
		fmt.Printf("  ▶ scope %s: %s\n", f.NodeName, f.Phase)

	case observability.KindBoundary:
		if _, isHandler := f.Details[observability.AttrScopePath]; isHandler {
			fmt.Printf("  ⚡ handler %s: %s\n", f.NodeName, f.Phase)
		}
	}
}
