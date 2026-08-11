// Command timer-event demonstrates a timer Start Event driving a ServiceTask:
// the process is instantiated when the timer fires (here, 5 seconds), runs a
// service task, and ends.
//
//	(timer start — fires in 5s) ◷─> handle-timeout (ServiceTask) ─> end
package main

import (
	"time"

	"fmt"
	"log"

	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// timerDelay is how long the start event's timer holds the token. The timer
// definition and the example's own assertion both read it, so the check can
// never drift away from the behavior it is checking.
const timerDelay = 5 * time.Second

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print(`
  timer-event:
    (timer start — fires in 5s) ◷─> handle-timeout (ServiceTask) ─> end

`)

	engine, err := thresher.New("timer-event-engine")
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	proc, err := buildProcess()
	if err != nil {
		return err
	}

	return runProcess(engine, proc)
}
