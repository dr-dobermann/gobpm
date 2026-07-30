// Command bpmn-convert is the SRD-051 §FR-9 front door: blank-import the BPMN
// converter, Import a bundled .bpmn, RegisterProcess + run it to completion on
// a thresher, then Export it back to stdout.
//
//	go run ./examples/bpmn-convert
//
// The bundled linear.bpmn is start → task → end. ManualTask is a no-op
// pass-through in gobpm, so the instance completes without human input or
// evaluable conditions. The §6 approval fixture (userTask + exclusive
// conditions) is covered by convert/bpmn tests but is not a runnable demo
// until conditions are replaced with a compiled expression engine.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	_ "github.com/dr-dobermann/gobpm/pkg/convert/bpmn"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

//go:embed linear.bpmn
var linearBPMN []byte

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print(`
  bpmn-convert (SRD-051 §FR-9):
    import linear.bpmn → register → run to completion → export

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p, err := convert.Import(ctx, convert.BPMN, bytes.NewReader(linearBPMN))
	if err != nil {
		return fmt.Errorf("import: %w", err)
	}

	fmt.Printf("imported process id=%q name=%q nodes=%d flows=%d\n",
		p.ID(), p.Name(), len(p.Nodes()), len(p.Flows()))

	engine, err := thresher.New("bpmn-convert-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	reg, err := engine.RegisterProcess(p)
	if err != nil {
		return fmt.Errorf("register process: %w", err)
	}

	fmt.Printf("registered key=%q version=%d (ADR-019 version key = BPMN process id)\n",
		reg.Key(), reg.Version())

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	h, err := engine.StartLatest(p.ID())
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("wait completion: %w", err)
	}

	fmt.Printf("instance completed: %s\n", state)

	fmt.Println("--- exported BPMN ---")

	if err := convert.Export(ctx, convert.BPMN, os.Stdout, p); err != nil {
		return fmt.Errorf("export: %w", err)
	}

	fmt.Println()

	return nil
}
