// Command process-data demonstrates the process data model (ADR-010 /
// SRD-007): a process property lives in the instance's container scope, two
// parallel branches read it through their own execution frames, each branch
// produces its result through its frame, and the results reach the bound
// DataObjects when the frames commit.
//
//	start ─> split ─┬─> greet-a ─> end-a       (result-a DataObject)
//	                └─> greet-b ─> end-b       (result-b DataObject)
package main

import (
	"fmt"
	"log"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print(`
  process-data:
    start ─> split ─┬─> greet-a ─> end-a       (result-a DataObject)
                    └─> greet-b ─> end-b       (result-b DataObject)

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("create default states: %w", err)
	}

	engine, err := thresher.New("process-data-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	done := make(chan string, 2)

	d, err := buildProcess(done)
	if err != nil {
		return err
	}

	return runProcess(engine, d, done)
}
