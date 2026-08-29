package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// buildTransaction assembles the booking Transaction Sub-Process:
//
//	start → [reserve-seat] → [charge-card] → cancel-booking (Cancel End)
//	            ╳ release-seat   ╳ refund-card    (Compensation boundaries)
//
// reserve and charge complete and enter the completion ledger; the Cancel End
// Event then aborts the Transaction.
//
// txOpts are the Transaction's own BPMN attributes (ADR-028 §2.7): the abort
// method — compensate unless stated — and the coordination protocol, held
// as stated and never read by the engine. The example prints what the model
// holds so the read-back is visible.
func buildTransaction(
	log *runLog, txOpts ...activities.TransactionOption,
) (*activities.SubProcess, error) {
	tx, err := activities.NewSubProcess("booking",
		activities.WithTransaction(txOpts...))
	if err != nil {
		return nil, fmt.Errorf("new transaction: %w", err)
	}

	tc := tx.Transaction()
	fmt.Printf("  booking: method=%q protocol=%q\n", tc.Method(), tc.Protocol())

	sStart, err := events.NewStartEvent("s-start")
	if err != nil {
		return nil, fmt.Errorf("s-start: %w", err)
	}

	reserve, err := stepTask(log, "reserve-seat", "  ✓ seat reserved")
	if err != nil {
		return nil, err
	}

	charge, err := stepTask(log, "charge-card", "  ✓ card charged")
	if err != nil {
		return nil, err
	}

	release, err := undoTask(log, "release-seat", "  ↩ seat released")
	if err != nil {
		return nil, err
	}

	refund, err := undoTask(log, "refund-card", "  ↩ card refunded")
	if err != nil {
		return nil, err
	}

	bndReserve, err := compBoundary("comp-reserve", reserve, release)
	if err != nil {
		return nil, err
	}

	bndCharge, err := compBoundary("comp-charge", charge, refund)
	if err != nil {
		return nil, err
	}

	cancEd, err := events.NewCancelEventDefinition()
	if err != nil {
		return nil, fmt.Errorf("cancel definition: %w", err)
	}

	cancelBooking, err := events.NewEndEvent("cancel-booking",
		events.WithCancelTrigger(cancEd))
	if err != nil {
		return nil, fmt.Errorf("cancel-booking: %w", err)
	}

	for _, e := range []flow.Element{
		sStart, reserve, charge, cancelBooking,
		bndReserve, release, bndCharge, refund,
	} {
		if err := tx.Add(e); err != nil {
			return nil, fmt.Errorf("add %s: %w", e.ID(), err)
		}
	}

	for _, l := range [][2]flow.Element{
		{sStart, reserve}, {reserve, charge}, {charge, cancelBooking},
	} {
		if err := link(l[0], l[1]); err != nil {
			return nil, err
		}
	}

	return tx, nil
}

// buildProcess wraps the booking Transaction with its Cancel handling:
//
//	start → (booking Transaction) → end
//	              ⚡ cancel-bnd → [notify-customer] → cx-end
//
// The Cancel End inside the Transaction aborts it — refund-card compensates
// before release-seat (reverse completion order) — then control leaves through
// the interrupting Cancel boundary to notify-customer.
func buildProcess(
	log *runLog, txOpts ...activities.TransactionOption,
) (*process.Process, error) {
	tx, err := buildTransaction(log, txOpts...)
	if err != nil {
		return nil, err
	}

	cbEd, err := events.NewCancelEventDefinition()
	if err != nil {
		return nil, fmt.Errorf("cancel boundary definition: %w", err)
	}

	cancelBnd, err := events.NewBoundaryEvent("cancel-bnd", tx, cbEd, true)
	if err != nil {
		return nil, fmt.Errorf("cancel boundary: %w", err)
	}

	notify, err := stepTask(log, "notify-customer",
		"  ✉ customer notified: booking canceled")
	if err != nil {
		return nil, err
	}

	proc, err := process.New("transaction-sub-process")
	if err != nil {
		return nil, fmt.Errorf("new process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("end: %w", err)
	}

	cxEnd, err := events.NewEndEvent("cx-end")
	if err != nil {
		return nil, fmt.Errorf("cx-end: %w", err)
	}

	for _, e := range []flow.Element{start, tx, cancelBnd, notify, end, cxEnd} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add %s: %w", e.ID(), err)
		}
	}

	for _, l := range [][2]flow.Element{
		{start, tx}, {tx, end}, {cancelBnd, notify}, {notify, cxEnd},
	} {
		if err := link(l[0], l[1]); err != nil {
			return nil, err
		}
	}

	return proc, nil
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
