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

// messageName is the name both the producer and the consumer use; the broker
// matches subscribers by it.
const messageName = "order placed"

// orderKey builds the CorrelationKey both sides correlate on (ADR-016 v.1
// §2.2): a single "orderId" property whose value is the message payload read
// under itemID. Because the producer and the consumer derive the key from the
// same payload, the engine routes each order's message to (or instantiates) the
// matching handler — distinct orders get distinct handler instances.
//
// itemID is the payload's item id on each side (the producer's outgoing item,
// the consumer's incoming item); the derived value is the same.
func orderKey(itemID string) (*bpmncommon.CorrelationKey, error) {
	msgRef := bpmncommon.MustMessage(messageName,
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID(itemID)))

	path := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, itemID)
			if err != nil {
				return nil, fmt.Errorf("read %q from payload: %w", itemID, err)
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
