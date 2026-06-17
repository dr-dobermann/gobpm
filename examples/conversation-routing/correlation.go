package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

const (
	// orderMsg starts a handler and seeds its conversation key.
	orderMsg = "order placed"
	// paymentMsg is the follow-up routed back to the handler's conversation.
	paymentMsg = "payment received"
	// orderItem is the message item id the order id is carried under.
	orderItem = "order_in"
	// payItem is the message item id the payment is carried under.
	payItem = "pay_in"
)

// orderKey builds the handler's CorrelationKey: a single property reading the
// order id from the "order placed" payload (item orderItem). The keyed message
// start seeds the conversation with this value; the in-instance receiver then
// subscribes keyed to it so the matching "payment received" routes back.
func orderKey() (*bpmncommon.CorrelationKey, error) {
	msgRef := bpmncommon.MustMessage(orderMsg,
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID(orderItem)))

	path := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, orderItem)
			if err != nil {
				return nil, fmt.Errorf("read %q from payload: %w", orderItem, err)
			}

			return values.NewVariable(fmt.Sprint(d.Value().Get(ctx))), nil
		})

	re, err := bpmncommon.NewCorrelationPropertyRetrievalExpression(path, msgRef)
	if err != nil {
		return nil, fmt.Errorf("retrieval expression: %w", err)
	}

	prop, err := bpmncommon.NewCorrelationProperty("orderId", "string",
		[]bpmncommon.CorrelationPropertyRetrievalExpression{*re})
	if err != nil {
		return nil, fmt.Errorf("correlation property: %w", err)
	}

	key, err := bpmncommon.NewCorrelationKey("orderKey",
		[]bpmncommon.CorrelationProperty{*prop})
	if err != nil {
		return nil, fmt.Errorf("correlation key: %w", err)
	}

	return key, nil
}
