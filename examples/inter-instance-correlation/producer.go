package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// orders are the order ids process A publishes — distinct keys, so each
// instantiates its own handler instance.
var orders = []string{"ORD-1", "ORD-2"}

// newProducer builds the source process A:
//
//	begin ─▶ send ORD-1 ─▶ send ORD-2 ─▶ end
//
// Started explicitly, it publishes one "order placed" message per order. Each
// SendTask binds its order property and stamps the correlation key derived from
// that payload onto the envelope (the producer side of ADR-016 v.1 §2.2).
func newProducer() (*process.Process, error) {
	props := make([]*data.Property, 0, len(orders))
	for _, id := range orders {
		props = append(props, data.MustProperty(itemID(id),
			data.MustItemDefinition(values.NewVariable(id),
				foundation.WithID(itemID(id))),
			data.ReadyDataState))
	}

	proc, err := process.New("order-source", data.WithProperties(props...))
	if err != nil {
		return nil, fmt.Errorf("create source process: %w", err)
	}

	begin, err := events.NewStartEvent("begin")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	chain := []flow.Element{begin}

	for _, id := range orders {
		send, err := newSend(id)
		if err != nil {
			return nil, err
		}

		chain = append(chain, send)
	}

	end, err := events.NewEndEvent("sent")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	chain = append(chain, end)

	for _, e := range chain {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	for i := 0; i < len(chain)-1; i++ {
		if err := link(chain[i], chain[i+1]); err != nil {
			return nil, err
		}
	}

	return proc, nil
}

// newSend builds a SendTask that publishes "order placed" carrying the order
// id (bound from its property) and correlation-keyed on that payload.
func newSend(id string) (*activities.SendTask, error) {
	key, err := orderKey(itemID(id))
	if err != nil {
		return nil, err
	}

	send, err := activities.NewSendTask("send-"+id,
		bpmncommon.MustMessage(messageName,
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID(itemID(id)))),
		activities.WithoutParams(),
		activities.WithCorrelationKey(key))
	if err != nil {
		return nil, fmt.Errorf("create send task for %q: %w", id, err)
	}

	return send, nil
}

// itemID is the per-order data item id (the SendTask binds the property of this
// id as the message payload).
func itemID(orderID string) string {
	return "order_" + orderID
}
