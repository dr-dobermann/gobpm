package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// buildProcess assembles start → timer(when) → [report] → end.
//
// Every element id is PINNED (foundation.WithID): cross-engine recovery
// requires stable element identity — the recovering engine resolves the
// checkpoint's recorded node ids against ITS registration of the same
// process version.
func buildProcess(engine string, when time.Time) (*process.Process, error) {
	proc, err := process.New("shipment", foundation.WithID("shipment"))
	if err != nil {
		return nil, fmt.Errorf("new process: %w", err)
	}

	start, err := events.NewStartEvent("start",
		foundation.WithID("shipment-start"))
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	timeExpr, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(time.Time{})),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(when), nil
		})
	if err != nil {
		return nil, fmt.Errorf("time expression: %w", err)
	}

	tDef, err := events.NewTimerEventDefinition(timeExpr, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("timer definition: %w", err)
	}

	wait, err := events.NewIntermediateCatchEvent("wait-pickup", tDef,
		foundation.WithID("shipment-wait"))
	if err != nil {
		return nil, fmt.Errorf("timer catch: %w", err)
	}

	report, err := reportTask(engine)
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end", foundation.WithID("shipment-end"))
	if err != nil {
		return nil, fmt.Errorf("end: %w", err)
	}

	for _, e := range []flow.Element{start, wait, report, end} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add %q: %w", e.Name(), err)
		}
	}

	for _, pair := range [][2]flow.Element{
		{start, wait}, {wait, report}, {report, end},
	} {
		src, _ := pair[0].(flow.SequenceSource)
		trg, _ := pair[1].(flow.SequenceTarget)

		if _, err := flow.Link(src, trg); err != nil {
			return nil, fmt.Errorf("link: %w", err)
		}
	}

	return proc, nil
}

// reportTask prints which engine ran the continuation — the zombie
// engine-1 fires its in-memory copy too (effects are at-least-once,
// ADR-033 §2.3); only the RECOVERING engine's state survives, because
// the zombie's checkpoint saves are CAS-fenced.
func reportTask(engine string) (*activities.ServiceTask, error) {
	op, err := gooper.New("report",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			fmt.Printf("  [%s] the timer fired - the shipment goes out\n",
				engine)

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("report op: %w", err)
	}

	st, err := activities.NewServiceTask("report", op,
		activities.WithoutParams(), foundation.WithID("shipment-report"))
	if err != nil {
		return nil, fmt.Errorf("report task: %w", err)
	}

	return st, nil
}
