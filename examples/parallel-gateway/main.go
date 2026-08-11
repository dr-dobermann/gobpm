// Command parallel-gateway demonstrates the BPMN Parallel (AND) gateway
// (SRD-005): a diverging gateway forks every outgoing branch, the branches run
// concurrently, and a converging gateway synchronizes them — the process
// continues only after every branch has arrived.
//
//	start ─> split ─┬─> worker-a ─┬─> join ─> end
//	                └─> worker-b ─┘
package main

import (
	"fmt"
	"log"

	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print(`
  parallel-gateway:
    start ─> split ─┬─> worker-a ─┬─> join ─> end
                    └─> worker-b ─┘

`)

	engine, err := thresher.New("parallel-gateway-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	// done reports which worker branch executed; runProcess waits for both,
	// proving the diverging gateway forked and ran the branches concurrently.
	done := make(chan string, 2)

	proc, err := buildProcess(done)
	if err != nil {
		return err
	}

	return runProcess(engine, proc, done)
}
