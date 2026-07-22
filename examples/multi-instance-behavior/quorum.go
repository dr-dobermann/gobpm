package main

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// completedAtLeast is the Complex behavior's quorum condition: it reads the §2.9
// numberOfCompletedInstances runtime attribute (published at the host scope on each
// completion) and holds once at least n reviewers have voted.
func completedAtLeast(n int) data.FormalExpression {
	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "numberOfCompletedInstances")
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(int)

			return values.NewVariable(v >= n), nil
		})
}
