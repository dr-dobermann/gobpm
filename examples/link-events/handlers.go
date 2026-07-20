package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// linkThrow builds a Link source (Intermediate Throw) for the given link name.
func linkThrow(id, name string) (*events.IntermediateThrowEvent, error) {
	def, err := events.NewLinkEventDefinition(name)
	if err != nil {
		return nil, fmt.Errorf("create link def %q: %w", name, err)
	}

	return events.NewIntermediateThrowEvent(id, def)
}

// linkCatch builds a Link target (Intermediate Catch) for the given link name.
func linkCatch(id, name string) (*events.IntermediateCatchEvent, error) {
	def, err := events.NewLinkEventDefinition(name)
	if err != nil {
		return nil, fmt.Errorf("create link def %q: %w", name, err)
	}

	return events.NewIntermediateCatchEvent(id, def)
}

// workTask prints one iteration and advances the shared counter each time the
// token reaches it through the Link redirect.
func workTask(count *int) (*activities.ServiceTask, error) {
	op, err := gooper.New("work",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			*count++
			fmt.Printf("  ▶ iteration %d (reached via the Link redirect)\n",
				*count)

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("create work operation: %w", err)
	}

	return activities.NewServiceTask("work", op, activities.WithoutParams())
}

// loopGateway builds the exclusive gateway and the exit condition count<3. The
// condition reads the same counter the work task advances (one track, so no
// shared-state race) — a fresh false→true edge is not needed here; the gateway
// re-evaluates every pass.
func loopGateway(
	count *int,
) (*gateways.ExclusiveGateway, data.FormalExpression, error) {
	xor, err := gateways.NewExclusiveGateway()
	if err != nil {
		return nil, nil, fmt.Errorf("create gateway: %w", err)
	}

	cond, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(*count < 3), nil
		})
	if err != nil {
		return nil, nil, fmt.Errorf("create loop condition: %w", err)
	}

	return xor, cond, nil
}
