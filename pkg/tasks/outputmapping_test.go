package tasks_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	exprengine "github.com/dr-dobermann/gobpm/pkg/model/expression/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/stretchr/testify/require"
)

// bodyValue builds a FormalExpression that extracts the body's value — the shape
// a WithOutputMapping rule's Path takes (SRD-037 FR-7).
func bodyValue(t *testing.T) data.FormalExpression {
	t.Helper()

	fe, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			b, err := ds.Find(ctx, "body")
			if err != nil {
				return nil, err
			}

			return values.NewVariable(b.Value().Get(ctx)), nil
		})
	require.NoError(t, err)

	return fe
}

func TestApplyOutputMappingExtracts(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	body := data.MustItemDefinition(values.NewVariable("order-42"),
		foundation.WithID("body"))

	out, err := tasks.ApplyOutputMapping(context.Background(), exprengine.New(),
		[]tasks.OutputRule{{Path: bodyValue(t), Var: "orderId"}}, body)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, "orderId", out[0].Name())
	require.Equal(t, "order-42", out[0].Value().Get(context.Background()))
}

// TestApplyOutputMappingRequiredMissingFaults: a required path the body doesn't
// satisfy (its evaluation errors) is a fault.
func TestApplyOutputMappingRequiredMissingFaults(t *testing.T) {
	_, err := tasks.ApplyOutputMapping(context.Background(), exprengine.New(),
		[]tasks.OutputRule{
			{Path: readName(t, "nope", "x"), Var: "v", Required: true}}, nil)
	require.Error(t, err)
}

// TestApplyOutputMappingOptionalMissingSkips: an optional path that fails to
// evaluate is skipped (no output, no error).
func TestApplyOutputMappingOptionalMissingSkips(t *testing.T) {
	out, err := tasks.ApplyOutputMapping(context.Background(), exprengine.New(),
		[]tasks.OutputRule{
			{Path: readName(t, "nope", "x"), Var: "v"}}, nil)
	require.NoError(t, err)
	require.Empty(t, out)
}
