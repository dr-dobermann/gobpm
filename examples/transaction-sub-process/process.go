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
func buildTransaction() (*activities.SubProcess, error) {
	tx, err := activities.NewSubProcess("booking", activities.WithTransaction())
	if err != nil {
		return nil, fmt.Errorf("new transaction: %w", err)
	}

	sStart, err := events.NewStartEvent("s-start")
	if err != nil {
		return nil, fmt.Errorf("s-start: %w", err)
	}

	reserve, err := stepTask("reserve-seat", "  ✓ seat reserved")
	if err != nil {
		return nil, err
	}

	charge, err := stepTask("charge-card", "  ✓ card charged")
	if err != nil {
		return nil, err
	}

	release, err := undoTask("release-seat", "  ↩ seat released")
	if err != nil {
		return nil, err
	}

	refund, err := undoTask("refund-card", "  ↩ card refunded")
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
		if _, err := flow.Link(
			l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget)); err != nil {
			return nil, fmt.Errorf("link: %w", err)
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
func buildProcess() (*process.Process, error) {
	tx, err := buildTransaction()
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

	notify, err := stepTask("notify-customer",
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
		if _, err := flow.Link(
			l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget)); err != nil {
			return nil, fmt.Errorf("link: %w", err)
		}
	}

	return proc, nil
}
