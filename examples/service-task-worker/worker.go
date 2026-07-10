package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/dr-dobermann/gobpm/pkg/tasks/localdispatcher"
)

// reserveWorker is the reserve-stock handler: a flaky inventory service that
// times out on a job's first two attempts (a plain technical error) and succeeds
// on the third, returning a {reservationId} body. Under WorkerTrusted the pool
// retries the timeouts in-process; the engine only ever sees the success.
func reserveWorker() localdispatcher.WorkerFunc {
	var (
		mu      sync.Mutex
		tries   = map[tasks.JobID]int{}
		lastRID int
	)

	return func(ctx context.Context, lj tasks.LockedJob) (*data.ItemDefinition, error) {
		mu.Lock()
		tries[lj.ID]++
		n := tries[lj.ID]
		mu.Unlock()

		if n < 3 {
			fmt.Printf("  reserve attempt %d: inventory timeout — worker retries in-process…\n", n)

			return nil, errors.New("inventory service timeout")
		}

		mu.Lock()
		lastRID++
		rid := fmt.Sprintf("R-%d", 1000+lastRID)
		mu.Unlock()

		fmt.Printf("  reserve attempt %d: reserved (reservationId=%s)\n", n, rid)

		return data.MustItemDefinition(values.NewVariable(rid),
			foundation.WithID("body")), nil
	}
}

// authorizeWorker is the authorize-payment handler: it reads the bound «amount»
// and reports a verdict — a Business Error for a gateway outage (amount < 0), or
// a Business Status DECLINED / AUTHORIZED — using a tasks.WorkerError, not a
// thrown technical error. Under WorkerTrusted the pool forwards the verdict as-is.
func authorizeWorker() localdispatcher.WorkerFunc {
	return func(ctx context.Context, lj tasks.LockedJob) (*data.ItemDefinition, error) {
		amount := 0
		if lj.Input != nil {
			amount, _ = lj.Input.Structure().Get(ctx).(int)
		}

		switch {
		case amount < 0:
			fmt.Println("  authorize: PaymentGatewayDown (Business Error) → boundary")

			return nil, &tasks.WorkerError{
				BpmnErrorCode: "PaymentGatewayDown",
				Message:       "payment gateway unreachable",
			}

		case amount > 1000:
			fmt.Println("  authorize: DECLINED (Business Status)")

			return nil, &tasks.WorkerError{Status: values.NewVariable("DECLINED")}

		default:
			fmt.Println("  authorize: AUTHORIZED (Business Status)")

			return nil, &tasks.WorkerError{Status: values.NewVariable("AUTHORIZED")}
		}
	}
}
