package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// buildProcess assembles the timeout-via-boundary demo:
//
//	start → [process-payment] ───────────────> end-paid
//	             ╳ (timer boundary, 2s, interrupting)
//	             └─> [cancel-order] ─────────> end-canceled
//
// The 2s interrupting timer boundary fires before the ~4s payment finishes, so the
// engine cancels the payment track, discards its result, and routes a token onto the
// boundary's exception flow to cancel-order — the canonical "timeout on a long task".
func buildProcess(ran *pathSet) (*process.Process, error) {
	proc, err := process.New("boundary-events")
	if err != nil {
		return nil, fmt.Errorf("new process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	payment, err := paymentTask(ran)
	if err != nil {
		return nil, err
	}

	cancelOrder, err := printTask(ran, "cancel-order",
		"  → cancel-order: payment timed out, releasing the reservation")
	if err != nil {
		return nil, err
	}

	endPaid, err := events.NewEndEvent("end-paid")
	if err != nil {
		return nil, fmt.Errorf("end-paid: %w", err)
	}

	endCancelled, err := events.NewEndEvent("end-canceled")
	if err != nil {
		return nil, fmt.Errorf("end-canceled: %w", err)
	}

	boundary, err := timerBoundary("payment-timeout", payment, 2*time.Second)
	if err != nil {
		return nil, err
	}

	for _, e := range []flow.Element{
		start, payment, cancelOrder, endPaid, endCancelled, boundary,
	} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add %s: %w", e.ID(), err)
		}
	}

	for _, l := range [][2]flow.Element{
		{start, payment},
		{payment, endPaid},
		{boundary, cancelOrder},
		{cancelOrder, endCancelled},
	} {
		if err := link(l[0], l[1]); err != nil {
			return nil, err
		}
	}

	return proc, nil
}

// timerBoundary builds an interrupting timer boundary that fires d after the guarded
// activity it is attached to is entered (the timer is registered when the token
// arrives on the host).
func timerBoundary(
	id string, host flow.ActivityNode, d time.Duration,
) (*events.BoundaryEvent, error) {
	when := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(time.Now().Add(d))),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(time.Now().Add(d)), nil
		},
		foundation.WithID(id+"-at"))

	def, err := events.NewTimerEventDefinition(when, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("timer def %q: %w", id, err)
	}

	be, err := events.NewBoundaryEvent(id, host, def, true) // interrupting
	if err != nil {
		return nil, fmt.Errorf("boundary %q: %w", id, err)
	}

	return be, nil
}

// link connects two flow elements with a sequence flow, reporting an element
// that cannot carry one rather than panicking on the assertion.
func link(src, trg flow.Element) error {
	s, ok := src.(flow.SequenceSource)
	if !ok {
		return fmt.Errorf("%q is not a sequence source", src.Name())
	}

	t, ok := trg.(flow.SequenceTarget)
	if !ok {
		return fmt.Errorf("%q is not a sequence target", trg.Name())
	}

	if _, err := flow.Link(s, t); err != nil {
		return fmt.Errorf("link: %w", err)
	}

	return nil
}
