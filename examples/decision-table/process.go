package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// buildProcess assembles the order flow: the Business Rule Task evaluates
// the deployed "discount" table; a reporting task prints the outcome.
//
//	start → [classify (BRT: decision "discount")] → [report] → end
func buildProcess(total int, tier string) (*process.Process, error) {
	proc, err := process.New("decision-table",
		data.WithProperties(
			data.MustProperty("total",
				data.MustItemDefinition(values.NewVariable(total),
					foundation.WithID("total")),
				data.ReadyDataState),
			data.MustProperty("tier",
				data.MustItemDefinition(values.NewVariable(tier),
					foundation.WithID("tier")),
				data.ReadyDataState)))
	if err != nil {
		return nil, fmt.Errorf("new process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	classify, err := activities.NewBusinessRuleTask("classify", "discount")
	if err != nil {
		return nil, fmt.Errorf("business rule task: %w", err)
	}

	report, err := reportTask()
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("end: %w", err)
	}

	for _, e := range []flow.Element{start, classify, report, end} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add %q: %w", e.Name(), err)
		}
	}

	for _, pair := range [][2]flow.Element{
		{start, classify}, {classify, report}, {report, end},
	} {
		src, _ := pair[0].(flow.SequenceSource)
		trg, _ := pair[1].(flow.SequenceTarget)

		if _, err := flow.Link(src, trg); err != nil {
			return nil, fmt.Errorf("link: %w", err)
		}
	}

	return proc, nil
}

// reportTask prints the decision outcome the BRT committed.
func reportTask() (*activities.ServiceTask, error) {
	op, err := gooper.New("report",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			pct, err := r.GetData("discount_pct")
			if err != nil {
				return nil, err
			}

			fmt.Printf("  [report] discount=%v%%\n", pct.Value().Get(ctx))

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("report op: %w", err)
	}

	st, err := activities.NewServiceTask("report", op,
		activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("report task: %w", err)
	}

	return st, nil
}
