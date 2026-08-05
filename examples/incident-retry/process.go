package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
)

// attempts counts the charge executions across every attempt — the failed
// ones, the policy retry and the operator's.
var attempts atomic.Int32

// healAt is the attempt that finally succeeds: the first two fail (the raise
// and the policy retry), the operator's third lands.
const healAt = 3

// chargeOp is the flaky payment call.
func chargeOp() (service.Operation, error) {
	return gooper.New("charge-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			n := attempts.Add(1)
			if n < healAt {
				return nil, fmt.Errorf(
					"payment gateway unavailable (attempt %d)", n)
			}

			fmt.Printf("→ charge succeeded on attempt %d\n", n)

			return nil, nil
		})
}

// buildProcess models start → charge → end. The charge carries an incident
// retry policy of exactly one automatic attempt, and the process carries an
// order id — visible in the incident's failure-time data snapshot.
func buildProcess() (*process.Process, error) {
	if err := data.CreateDefaultStates(); err != nil {
		return nil, fmt.Errorf("default states: %w", err)
	}

	p, err := process.New("payment",
		data.WithProperties(
			data.MustProperty("order_id",
				data.MustItemDefinition(values.NewVariable("ORD-42"),
					foundation.WithID("order_id")),
				data.ReadyDataState)))
	if err != nil {
		return nil, fmt.Errorf("process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	op, err := chargeOp()
	if err != nil {
		return nil, fmt.Errorf("charge op: %w", err)
	}

	charge, err := activities.NewServiceTask("charge", op,
		activities.WithoutParams(),
		activities.WithIncidentRetryPolicy(
			tasks.FixedDelay(2, 150*time.Millisecond)))
	if err != nil {
		return nil, fmt.Errorf("charge task: %w", err)
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("end: %w", err)
	}

	for _, e := range []flow.Element{start, charge, end} {
		if err := p.Add(e); err != nil {
			return nil, fmt.Errorf("add %q: %w", e.Name(), err)
		}
	}

	if _, err := flow.Link(start, charge); err != nil {
		return nil, fmt.Errorf("link start: %w", err)
	}

	if _, err := flow.Link(charge, end); err != nil {
		return nil, fmt.Errorf("link charge: %w", err)
	}

	return p, nil
}
