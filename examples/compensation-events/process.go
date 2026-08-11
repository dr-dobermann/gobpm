package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// buildProcess assembles the trip-booking saga:
//
//	start → [book-hotel] → [book-flight] → [cancel-trip] (Compensation End)
//	            ╳ undo-hotel   ╳ undo-flight    (Compensation boundaries)
//
// Both bookings complete and enter the completion ledger (with a data
// snapshot each). The Compensation End Event then compensates the whole
// scope in REVERSE completion order — undo-flight runs before undo-hotel —
// waiting for both handlers before the instance completes.
func buildProcess(log *runLog) (*process.Process, error) {
	proc, err := process.New("compensation-events")
	if err != nil {
		return nil, fmt.Errorf("new process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	hotel, err := bookTask(log, "book-hotel", "  ✓ hotel booked")
	if err != nil {
		return nil, err
	}

	flight, err := bookTask(log, "book-flight", "  ✓ flight booked")
	if err != nil {
		return nil, err
	}

	undoHotel, err := undoTask(log, "undo-hotel", "  ↩ hotel booking canceled")
	if err != nil {
		return nil, err
	}

	undoFlight, err := undoTask(log, "undo-flight", "  ↩ flight booking canceled")
	if err != nil {
		return nil, err
	}

	bndHotel, err := compBoundary("comp-hotel", hotel, undoHotel)
	if err != nil {
		return nil, err
	}

	bndFlight, err := compBoundary("comp-flight", flight, undoFlight)
	if err != nil {
		return nil, err
	}

	ced, err := events.NewCompensationEventDefinition(nil, true)
	if err != nil {
		return nil, fmt.Errorf("throw definition: %w", err)
	}

	cancelTrip, err := events.NewEndEvent("cancel-trip",
		events.WithCompensationTrigger(ced))
	if err != nil {
		return nil, fmt.Errorf("cancel-trip: %w", err)
	}

	for _, e := range []flow.Element{
		start, hotel, flight, cancelTrip,
		bndHotel, undoHotel, bndFlight, undoFlight,
	} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add %s: %w", e.ID(), err)
		}
	}

	for _, l := range [][2]flow.Element{
		{start, hotel},
		{hotel, flight},
		{flight, cancelTrip},
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
