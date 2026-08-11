package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// newGreeter builds a ServiceTask whose Go operation reads the user_name
// process property (by plain name) and the engine's STARTED_AT runtime
// variable (by its RUNTIME path) through the public data reader, produces a
// greeting, and returns it — plus the DataObject its output association feeds.
func newGreeter(
	name, resID, greeting string,
	done chan<- string,
) (*activities.ServiceTask, *dataobjects.DataObject, error) {
	op, err := gooper.New(
		name+"-op",
		func(ctx context.Context, r service.DataReader, _ *data.ItemDefinition) (*data.ItemDefinition, error) {
			// the process property, by plain name ...
			who, err := r.GetData("user_name")
			if err != nil {
				return nil, fmt.Errorf("read user_name: %w", err)
			}

			// ... and an engine runtime variable, by its RUNTIME path.
			started, err := r.GetData("RUNTIME/STARTED_AT")
			if err != nil {
				return nil, fmt.Errorf("read RUNTIME/STARTED_AT: %w", err)
			}

			res := fmt.Sprintf("%s, %s!", greeting, who.Value().Get(ctx))

			fmt.Printf("  ▶ %s produced %q (instance started %v)\n",
				name, res, started.Value().Get(ctx))
			done <- name

			return data.MustItemDefinition(
					values.NewVariable(res),
					foundation.WithID(resID)),
				nil
		})
	if err != nil {
		return nil, nil, fmt.Errorf("create %s operation: %w", name, err)
	}

	// declare the task output the operation result fills (the producer
	// stage copies the frame put into this output's per-execution instance).
	outParam := data.MustParameter(name+" result",
		data.MustItemAwareElement(
			data.MustItemDefinition(
				values.NewVariable(""),
				foundation.WithID(resID)),
			data.UnavailableDataState))

	st, err := activities.NewServiceTask(name, op,
		activities.WithParameters(data.Output, outParam))
	if err != nil {
		return nil, nil, fmt.Errorf("create %s task: %w", name, err)
	}

	// the DataObject the branch result lands in via the output association.
	resDO, err := dataobjects.New(name+"-result",
		data.MustItemDefinition(
			values.NewVariable(""),
			foundation.WithID(resID)),
		nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create %s result object: %w", name, err)
	}

	if err := resDO.AssociateSource(st, []string{resID}, nil); err != nil {
		return nil, nil, fmt.Errorf("bind %s result object: %w", name, err)
	}

	return st, resDO, nil
}

// link connects two flow elements with a sequence flow.
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
