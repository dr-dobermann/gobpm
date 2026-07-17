package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// callPrinter surfaces the call-activity lifecycle: Started when the caller
// launches the child, Completed/Failed when it ends — each carrying the
// resolved version and the child instance id (SRD-050 FR-10).
type callPrinter struct{}

// OnFact prints one line per Call fact.
func (callPrinter) OnFact(f observability.Fact) {
	if f.Kind != observability.KindCall {
		return
	}

	fmt.Printf("  ▶ call %s: %s (%s v.%s → instance %s)\n",
		f.NodeName, f.Phase,
		f.Details[observability.AttrCalledKey],
		f.Details[observability.AttrCalledVersion],
		f.Details[observability.AttrChildInstanceID])
}
