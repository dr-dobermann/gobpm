package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/rules"
	"github.com/dr-dobermann/gobpm/pkg/rules/gorules"
)

// buildEngine assembles the batteries-included Business Rule Engine: the
// gorules registry with the "discount" decision — read the order total from
// process data, yield the discount percent as one result row. The task's
// 1-row/1-output fold commits it as the scalar variable "discount_pct".
func buildEngine() (*gorules.Registry, error) {
	reg := gorules.New()

	err := reg.Register("discount",
		func(ctx context.Context, r service.DataReader) (rules.Row, error) {
			d, err := r.GetData("total")
			if err != nil {
				return nil, err
			}

			total, _ := d.Value().Get(ctx).(int)

			pct := 5
			if total > 100 {
				pct = 15
			}

			fmt.Printf("  [decision discount] total=%d -> discount_pct=%d\n",
				total, pct)

			return rules.Row{"discount_pct": values.NewVariable(pct)}, nil
		})
	if err != nil {
		return nil, fmt.Errorf("register decision: %w", err)
	}

	return reg, nil
}

// buildLanes creates the two service tasks announcing which discount lane the
// decision routed the order into.
func buildLanes() (*activities.ServiceTask, *activities.ServiceTask, error) {
	big, err := announceTask("apply-big-discount",
		"  [apply-big-discount] wholesale rate applied")
	if err != nil {
		return nil, nil, err
	}

	small, err := announceTask("apply-small-discount",
		"  [apply-small-discount] retail rate applied")
	if err != nil {
		return nil, nil, err
	}

	return big, small, nil
}
