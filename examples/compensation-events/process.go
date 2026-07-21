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
func buildProcess() (*process.Process, error) {
	proc, err := process.New("compensation-events")
	if err != nil {
		return nil, fmt.Errorf("new process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	hotel, err := bookTask("book-hotel", "  ✓ hotel booked")
	if err != nil {
		return nil, err
	}

	flight, err := bookTask("book-flight", "  ✓ flight booked")
	if err != nil {
		return nil, err
	}

	undoHotel, err := undoTask("undo-hotel", "  ↩ hotel booking canceled")
	if err != nil {
		return nil, err
	}

	undoFlight, err := undoTask("undo-flight", "  ↩ flight booking canceled")
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
		if _, err := flow.Link(
			l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget)); err != nil {
			return nil, fmt.Errorf("link: %w", err)
		}
	}

	return proc, nil
}
