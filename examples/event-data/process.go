package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

const (
	orderMsgName = "order placed"
	quoteMsgName = "quote ready"
)

// buildQuote assembles the message-started "quote" process and wires its
// events' data (ADR-040 v.2 §2.7, the standard's Start/End special case):
//
//	start[order placed] ──▶ process input "order" (and data object "received")
//	price: reads order, writes total
//	end[quote ready]    ◀── process output "total"
func buildQuote() (*process.Process, error) {
	p, err := process.New("quote",
		data.WithInputs(param("order", "")),
		data.WithOutputs(param("total", 0)))
	if err != nil {
		return nil, err
	}

	start, err := events.NewStartEvent("start",
		events.WithMessageTrigger(events.MustMessageEventDefinition(
			bpmncommon.MustMessage(orderMsgName, item("order_in", "")), nil)))
	if err != nil {
		return nil, err
	}

	// the end's data input is declared Unavailable and required: only the
	// association from the process output can make the throw fire
	end, err := events.NewEndEvent("end",
		events.WithMessageTrigger(events.MustMessageEventDefinition(
			bpmncommon.MustMessage(quoteMsgName, item("quote_out", 0)), nil)),
		events.WithDataInputs(data.MustParameter("quote",
			data.MustItemAwareElement(item("quote_out", 0), data.UnavailableDataState))))
	if err != nil {
		return nil, err
	}

	received, err := dataobjects.New("received", item("received-item", ""), nil)
	if err != nil {
		return nil, err
	}

	price, err := priceTask()
	if err != nil {
		return nil, err
	}

	if err := wire(p, start, price, end); err != nil {
		return nil, err
	}

	if err := p.Add(received); err != nil {
		return nil, err
	}

	// the three wirings: start → process input, start → data object,
	// process output → end
	if err := p.AssociateInput("order", start, "order_in"); err != nil {
		return nil, fmt.Errorf("wire the start into the input: %w", err)
	}

	if err := received.AssociateSource(start, []string{"order_in"}, nil); err != nil {
		return nil, fmt.Errorf("wire the start into the data object: %w", err)
	}

	if err := p.AssociateOutput("total", end, end.Inputs()[0].ID()); err != nil {
		return nil, fmt.Errorf("wire the output into the end: %w", err)
	}

	return p, nil
}
