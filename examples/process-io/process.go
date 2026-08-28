package main

import (
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

const rateKey = "rate"

// buildRate assembles the reusable child with its own contract: it takes
// "amount" and promises "total". A caller reaches it only through these two
// names — the declaration IS the call boundary (ADR-040 §2.4).
func buildRate() (*process.Process, error) {
	p, err := process.New("rate", foundation.WithID(rateKey),
		data.WithInputs(param("amount", 0)),
		data.WithOutputs(param("total", 0)))
	if err != nil {
		return nil, err
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, err
	}

	tax, err := taxTask()
	if err != nil {
		return nil, err
	}

	return p, wire(p, start, tax, mustEnd())
}

// buildQuote assembles the host-facing process: start → price[calls rate]
// → stamp → end. Its contract: "amount" is required, "currency" is optional
// (an absent optional input is simply not in the scope); "total" must be
// produced by the end, "started_at" may be.
func buildQuote(got *seen) (*process.Process, error) {
	p, err := process.New("quote",
		data.WithInputs(
			param("amount", 0),
			param("currency", "", data.Optional())),
		data.WithOutputs(
			param("total", 0),
			param("started_at", time.Time{}, data.Optional())))
	if err != nil {
		return nil, err
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, err
	}

	// the call parameters name the child's declared inputs and outputs:
	// "amount" crosses in, "total" comes back (by-name correspondence).
	price, err := activities.NewCallActivity("price", rateKey,
		activities.WithParameters(data.Input, param("amount", 0)),
		activities.WithParameters(data.Output, param("total", 0)))
	if err != nil {
		return nil, err
	}

	stamp, err := stampTask(got)
	if err != nil {
		return nil, err
	}

	return p, wire(p, start, price, stamp, mustEnd())
}

// param builds a Ready parameter whose item carries the value's type — the
// same helper declares a process slot and a call parameter.
func param(name string, zero any, opts ...data.ParameterOption) *data.Parameter {
	return data.MustParameter(name,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(zero),
				foundation.WithID(name)),
			data.ReadyDataState),
		opts...)
}

// mustEnd builds an end event or panics (example brevity).
func mustEnd() *events.EndEvent {
	e, err := events.NewEndEvent("end")
	if err != nil {
		panic(err)
	}

	return e
}

// wire adds the nodes to p and links them in sequence.
func wire(p *process.Process, nodes ...flow.Element) error {
	for _, n := range nodes {
		if err := p.Add(n); err != nil {
			return err
		}
	}

	for i := 0; i+1 < len(nodes); i++ {
		s, ok := nodes[i].(flow.SequenceSource)
		if !ok {
			return fmt.Errorf("%q is not a sequence source", nodes[i].Name())
		}

		t, ok := nodes[i+1].(flow.SequenceTarget)
		if !ok {
			return fmt.Errorf("%q is not a sequence target", nodes[i+1].Name())
		}

		if _, err := flow.Link(s, t); err != nil {
			return fmt.Errorf("link: %w", err)
		}
	}

	return nil
}
