package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

type dataChangePrinter struct{}

func (p *dataChangePrinter) OnFact(f observability.Fact) {
	if f.Kind != observability.KindDataChange {
		return
	}

	fmt.Printf("  ▶ %s %s @%s\n",
		f.Phase, f.Details[observability.AttrDataPath], f.NodeName)
}
