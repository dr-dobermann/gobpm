package main

import (
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// orderData builds the process properties the expressions navigate: a
// nested record, a rates map and a deadline time.
func orderData() ([]*data.Property, error) {
	customer, err := values.NewRecord(
		values.F("tier", values.NewVariable("vip")))
	if err != nil {
		return nil, fmt.Errorf("customer record: %w", err)
	}

	order, err := values.NewRecord(
		values.F("total", values.NewVariable(500)),
		values.F("customer", customer))
	if err != nil {
		return nil, fmt.Errorf("order record: %w", err)
	}

	rates, err := values.NewMap(map[string]float64{
		"EUR": 1.09,
		"USD": 0.92,
	})
	if err != nil {
		return nil, fmt.Errorf("rates map: %w", err)
	}

	deadline := values.NewVariable(
		time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC))

	return []*data.Property{
		data.MustProperty("order",
			data.MustItemDefinition(order, foundation.WithID("order")),
			data.ReadyDataState),
		data.MustProperty("rates",
			data.MustItemDefinition(rates, foundation.WithID("rates")),
			data.ReadyDataState),
		data.MustProperty("deadline",
			data.MustItemDefinition(deadline,
				foundation.WithID("deadline")),
			data.ReadyDataState),
	}, nil
}
