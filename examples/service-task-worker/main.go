// Command service-task-worker demonstrates the external-worker execution model
// (ADR-021, SRD-035…039): a ServiceTask dispatched to a local pool worker, run in
// the default WorkerTrusted mode, showing in-process retries, output mapping, and
// Business Status / Business Error verdicts (state instead of thrown errors).
//
// See README.md for the full walk-through.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/tasks/localdispatcher"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// order is one scenario the demo runs.
type order struct {
	name   string
	amount int
}

func run() error {
	fmt.Print(`
  service-task-worker (WorkerTrusted):
    start → reserve-stock → authorize-payment → «paymentStatus» gateway / boundary

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build the in-process worker pool and register the two handlers; hand it to
	// the engine as its worker dispatcher.
	disp := localdispatcher.New(nil, 0)
	if err := disp.RegisterWorker(ctx, "reserve", reserveWorker()); err != nil {
		return fmt.Errorf("register reserve worker: %w", err)
	}

	if err := disp.RegisterWorker(ctx, "authorize", authorizeWorker()); err != nil {
		return fmt.Errorf("register authorize worker: %w", err)
	}

	engine, err := thresher.New("order-engine",
		thresher.WithWorkerDispatcher(disp),
		thresher.WithoutBanner(), thresher.WithoutStartupConfig())
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	if err := engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	for _, o := range []order{
		{"order-normal", 50},
		{"order-over-limit", 5000},
		{"order-gateway-down", -1},
	} {
		if err := runOrder(ctx, engine, o); err != nil {
			return err
		}
	}

	return nil
}

// runOrder builds, registers, and runs one order to completion, printing the
// worker's actions (from the handlers) and the final outcome.
func runOrder(ctx context.Context, engine *thresher.Thresher, o order) error {
	fmt.Printf("\n%s (amount %d):\n", o.name, o.amount)

	proc, err := buildOrderProcess(o.name, o.amount)
	if err != nil {
		return fmt.Errorf("build %s: %w", o.name, err)
	}

	if _, err := engine.RegisterProcess(proc); err != nil {
		return fmt.Errorf("register %s: %w", o.name, err)
	}

	h, err := engine.StartLatest(proc.ID())
	if err != nil {
		return fmt.Errorf("start %s: %w", o.name, err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		return fmt.Errorf("wait %s: %w", o.name, err)
	}

	fmt.Printf("  ✓ completed (%s) → %s\n", state, outcome(ctx, h))

	return nil
}

// outcome summarizes the final result from the instance's variables: the payment
// status the worker wrote (routed by the gateway), or — when it never wrote one —
// the Business Error the boundary caught.
func outcome(ctx context.Context, h *thresher.InstanceHandle) string {
	dr := h.Data()

	status, err := dr.GetData("paymentStatus")
	if err != nil {
		return "payment-failed (Business Error caught by the boundary)"
	}

	// reservationId and warehouseZone were extracted from the worker's
	// structured {reservationId, warehouse:{zone}} body by structural output
	// mapping (body.reservationId, body.warehouse.zone).
	rid := ""
	if r, rerr := dr.GetData("reservationId"); rerr == nil {
		rid = fmt.Sprintf(", reservationId=%v", r.Value().Get(ctx))

		if z, zerr := dr.GetData("warehouseZone"); zerr == nil {
			rid += fmt.Sprintf(", warehouseZone=%v", z.Value().Get(ctx))
		}
	}

	if s, _ := status.Value().Get(ctx).(string); s == "AUTHORIZED" {
		return fmt.Sprintf("shipped [paymentStatus=%s%s]", s, rid)
	}

	return fmt.Sprintf("held [paymentStatus=%v%s]", status.Value().Get(ctx), rid)
}
