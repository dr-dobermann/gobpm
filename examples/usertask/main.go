// Command usertask demonstrates a UserTask driven from the console: the engine
// parks the task, the console TaskDistributor Takes it, CLAIMS it for exclusive
// hold, renders its form, and Completes it, resuming the process to its end
// event. The run asserts that all three ownership phases actually happened.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/interactor/console"
	"github.com/dr-dobermann/gobpm/pkg/observability"
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
              └─ console driver: Take → Claim → render form → Complete
                 (claiming is required: only the holder may complete)

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

	// Watch the ownership lifecycle so the run can be checked, not just watched.
	watch := newLifecycleWatch()
	defer th.Observe(watch).Cancel()

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

	if state != thresher.StateCompleted {
		return fmt.Errorf("process finished %s, want %s",
			state, thresher.StateCompleted)
	}

	// The point of the example is the ownership flow: the task is announced to
	// the distributor, the driver takes exclusive hold, and only then completes.
	// Assert all three — a driver that quietly skipped the claim would otherwise
	// look identical from the outside.
	if absent := watch.missing(
		observability.PhaseAnnounced,
		observability.PhaseClaimed,
		observability.PhaseCompleted,
	); len(absent) > 0 {
		return fmt.Errorf("user task never reached %v", absent)
	}

	fmt.Println("process finished:", state)

	return nil
}
