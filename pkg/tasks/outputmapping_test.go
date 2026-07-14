package tasks_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	exprengine "github.com/dr-dobermann/gobpm/pkg/model/expression/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
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

// constExpr builds a FormalExpression that evaluates to a fixed value regardless
// of the source — it isolates the assembly under test (T-3) from body extraction.
func constExpr(t *testing.T, v any) data.FormalExpression {
	t.Helper()

	fe, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(v)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(v), nil
		})
	require.NoError(t, err)

	return fe
}

// TestOutputMappingAssembly (SRD-043 T-3): structural Vars sharing a head
// assemble one record; a plain Var still emits a whole datum; a plain+nested
// clash and a malformed Var are classified errors.
func TestOutputMappingAssembly(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	ee := exprengine.New()

	t.Run("nested head assembles one record; plain stays whole", func(t *testing.T) {
		out, err := tasks.ApplyOutputMapping(ctx, ee, []tasks.OutputRule{
			{Path: constExpr(t, 150), Var: "order.total"},
			{Path: constExpr(t, 9), Var: "order.items[0].price"},
			{Path: constExpr(t, "A-1"), Var: "orderId"},
		}, nil)
		require.NoError(t, err)
		require.Len(t, out, 2) // one datum per head, first-seen order

		require.Equal(t, "order", out[0].Name())
		total, err := data.WalkSteps(ctx, out[0].Value(),
			[]data.Step{{Field: "total"}})
		require.NoError(t, err)
		require.Equal(t, 150, total.Get(ctx))

		price, err := data.WalkSteps(ctx, out[0].Value(),
			[]data.Step{{Field: "items"}, {Index: 0}, {Field: "price"}})
		require.NoError(t, err)
		require.Equal(t, 9, price.Get(ctx))

		require.Equal(t, "orderId", out[1].Name())
		require.Equal(t, "A-1", out[1].Value().Get(ctx))
	})

	t.Run("plain and nested on one head clash", func(t *testing.T) {
		_, err := tasks.ApplyOutputMapping(ctx, ee, []tasks.OutputRule{
			{Path: constExpr(t, 1), Var: "x"},
			{Path: constExpr(t, 2), Var: "x.y"},
		}, nil)
		require.Error(t, err)
	})

	t.Run("conflicting structural paths on one head", func(t *testing.T) {
		// "order.total" makes total a scalar; "order.total.x" then tries to
		// descend into it — SetPath surfaces the conflict.
		_, err := tasks.ApplyOutputMapping(ctx, ee, []tasks.OutputRule{
			{Path: constExpr(t, 1), Var: "order.total"},
			{Path: constExpr(t, 2), Var: "order.total.x"},
		}, nil)
		require.Error(t, err)
	})

	t.Run("malformed Var path", func(t *testing.T) {
		_, err := tasks.ApplyOutputMapping(ctx, ee, []tasks.OutputRule{
			{Path: constExpr(t, 1), Var: "a..b"},
		}, nil)
		require.Error(t, err)
	})

	t.Run("nested head with only an optional miss yields nothing", func(t *testing.T) {
		out, err := tasks.ApplyOutputMapping(ctx, ee, []tasks.OutputRule{
			{Path: readName(t, "nope", "x"), Var: "order.total"},
		}, nil)
		require.NoError(t, err)
		require.Empty(t, out)
	})

	t.Run("required miss inside a nested head faults", func(t *testing.T) {
		_, err := tasks.ApplyOutputMapping(ctx, ee, []tasks.OutputRule{
			{Path: readName(t, "nope", "x"), Var: "order.total", Required: true},
		}, nil)
		require.Error(t, err)
	})
}

// TestOutputMappingPlainUnchanged (SRD-043 T-4): whole-value rules are emitted
// byte-identical, one datum per Var, in order — the pre-structural behavior.
func TestOutputMappingPlainUnchanged(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	out, err := tasks.ApplyOutputMapping(ctx, exprengine.New(), []tasks.OutputRule{
		{Path: constExpr(t, "order-42"), Var: "orderId"},
		{Path: constExpr(t, "PAID"), Var: "status"},
	}, nil)
	require.NoError(t, err)
	require.Len(t, out, 2)

	require.Equal(t, "orderId", out[0].Name())
	require.Equal(t, "order-42", out[0].Value().Get(ctx))
	require.Equal(t, "status", out[1].Name())
	require.Equal(t, "PAID", out[1].Value().Get(ctx))
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
