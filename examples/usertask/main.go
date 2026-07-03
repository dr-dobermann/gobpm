// Command usertask demonstrates a UserTask driven from the console: the engine
// parks the task, the console TaskDistributor Takes it, renders its form, and
// Completes it, resuming the process to its end event.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/interactor/console"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// operator is the acting human the console driver authorizes as.
type operator struct{}

func (operator) UserID() string   { return "operator" }
func (operator) Groups() []string { return nil }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Print(`
  usertask (console-driven approval):
    start → (approve: UserTask) → end
              │  candidateUsers: operator
              │  output: decision (string)
              └─ console driver: Take → render form → Complete

`)

	p, err := buildProcess()
	if err != nil {
		return err
	}

	// The console driver auto-completes each parked UserTask from the console.
	// Built first, passed to the engine, then bound to it.
	driver := console.New(operator{}, os.Stdout)

	th, err := thresher.New("approval-engine",
		thresher.WithTaskDistributor(driver))
	if err != nil {
		return err
	}

	driver.Bind(th)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := th.Run(ctx); err != nil {
		return err
	}

	if _, err := th.RegisterProcess(p); err != nil {
		return err
	}

	h, err := th.StartLatest(p.ID())
	if err != nil {
		return err
	}

	wctx, wc := context.WithTimeout(context.Background(), 5*time.Second)
	defer wc()

	state, err := h.WaitCompletion(wctx)
	if err != nil {
		return err
	}

	fmt.Println("process finished:", state)

	return nil
}
