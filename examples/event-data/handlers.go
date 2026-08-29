package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// priceTask reads the declared input "order" — the message payload the
// start's association bound there — and the data object "received" the
// same payload landed in, and writes "total" into the root scope, where
// the declared output is read at completion.
func priceTask() (*activities.ServiceTask, error) {
	op, err := gooper.New("price",
		func(ctx context.Context, ds service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			order, err := ds.GetData("order")
			if err != nil {
				return nil, err
			}

			received, err := ds.GetData("received")
			if err != nil {
				return nil, err
			}

			id, _ := order.Value().Get(ctx).(string)
			total := len(id) * 10

			fmt.Printf("  ▶ price: order=%q (received=%q) → total=%d\n",
				id, received.Value().Get(ctx), total)

			return data.MustItemDefinition(values.NewVariable(total),
				foundation.WithID("total")), nil
		})
	if err != nil {
		return nil, err
	}

	return activities.NewServiceTask("price", op, activities.WithoutParams())
}

// item builds an item over a variable holding zero, with the given id.
func item(id string, zero any) *data.ItemDefinition {
	return data.MustItemDefinition(values.NewVariable(zero), foundation.WithID(id))
}

// param declares a Ready parameter over an item named after it.
func param(name string, zero any) *data.Parameter {
	return data.MustParameter(name,
		data.MustItemAwareElement(item(name+"-item", zero), data.ReadyDataState))
}

// wire adds the nodes to p and links them in sequence.
func wire(p *process.Process, nodes ...flow.Element) error {
	for _, n := range nodes {
		if err := p.Add(n); err != nil {
			return err
		}
	}

	for i := 0; i+1 < len(nodes); i++ {
		s, _ := nodes[i].(flow.SequenceSource)
		t, _ := nodes[i+1].(flow.SequenceTarget)

		if s == nil || t == nil {
			return fmt.Errorf("%q → %q: not a sequence pair",
				nodes[i].Name(), nodes[i+1].Name())
		}

		if _, err := flow.Link(s, t); err != nil {
			return fmt.Errorf("link: %w", err)
		}
	}

	return nil
}
