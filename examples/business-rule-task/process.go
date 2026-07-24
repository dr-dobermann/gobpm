package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// buildProcess assembles the discount demo: the Business Rule Task evaluates
// the "discount" decision against the order total; the committed result
// (discount_pct) routes the task's own conditional flows:
//
//	start → [classify (BRT: decision "discount")]
//	           ├─ discount_pct > 10 ─> [apply-big-discount] ──> end-big
//	           └─ (default) ────────> [apply-small-discount] ─> end-small
func buildProcess(total int) (*process.Process, error) {
	proc, err := process.New("business-rule-task",
		data.WithProperties(
			data.MustProperty("total",
				data.MustItemDefinition(values.NewVariable(total),
					foundation.WithID("total")),
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

	big, small, err := buildLanes()
	if err != nil {
		return nil, err
	}

	endBig, err := events.NewEndEvent("end-big")
	if err != nil {
		return nil, fmt.Errorf("end-big: %w", err)
	}

	endSmall, err := events.NewEndEvent("end-small")
	if err != nil {
		return nil, fmt.Errorf("end-small: %w", err)
	}

	for _, e := range []flow.Element{
		start, classify, big, small, endBig, endSmall,
	} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add %q: %w", e.Name(), err)
		}
	}

	if _, err := flow.Link(start, classify); err != nil {
		return nil, fmt.Errorf("link start: %w", err)
	}

	cond, err := discountGt(10)
	if err != nil {
		return nil, fmt.Errorf("condition: %w", err)
	}

	if _, err := flow.Link(classify, big, flow.WithCondition(cond)); err != nil {
		return nil, fmt.Errorf("link big lane: %w", err)
	}

	sf, err := flow.Link(classify, small)
	if err != nil {
		return nil, fmt.Errorf("link small lane: %w", err)
	}

	if err := classify.SetDefaultFlow(sf.ID()); err != nil {
		return nil, fmt.Errorf("default flow: %w", err)
	}

	if _, err := flow.Link(big, endBig); err != nil {
		return nil, fmt.Errorf("link end-big: %w", err)
	}

	if _, err := flow.Link(small, endSmall); err != nil {
		return nil, fmt.Errorf("link end-small: %w", err)
	}

	return proc, nil
}

// discountGt builds the "discount_pct > n" condition over the decision result
// the Business Rule Task committed to process data.
func discountGt(n int) (data.FormalExpression, error) {
	return goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "discount_pct")
			if err != nil {
				return nil, err
			}

			pct, _ := d.Value().Get(ctx).(int)

			return values.NewVariable(pct > n), nil
		})
}
