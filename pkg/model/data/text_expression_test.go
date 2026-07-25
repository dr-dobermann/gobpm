package data_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// TestTextExpression covers SRD-066 T-3.
func TestTextExpression(t *testing.T) {
	ctx := context.Background()

	t.Run("construction validation",
		func(t *testing.T) {
			_, err := data.NewTextExpression(" ", "total > 100")
			require.Error(t, err)
			require.Contains(t, err.Error(), "language")

			_, err = data.NewTextExpression("gobpm:lite", "  ")
			require.Error(t, err)
			require.Contains(t, err.Error(), "body")

			_, err = data.NewTextExpression("gobpm:lite", "x",
				data.WithResultType("  "))
			require.Error(t, err)

			_, err = data.NewTextExpression("gobpm:lite", "x",
				data.WithKind(data.PhysicalKind)) // a foreign option kind
			require.Error(t, err)
		})

	t.Run("getters and identity",
		func(t *testing.T) {
			te, err := data.NewTextExpression(" gobpm:lite ", " total > 100 ",
				data.WithResultType("boolean"),
				foundation.WithID("cond-1"))
			require.NoError(t, err)

			require.Equal(t, "gobpm:lite", te.Language())
			require.Equal(t, "total > 100", te.Body())
			require.Equal(t, "boolean", te.ResultType())
			require.Equal(t, "cond-1", te.ID())
			require.False(t, te.IsEvaluated())
		})

	t.Run("self-evaluation refused loud",
		func(t *testing.T) {
			te, err := data.NewTextExpression("gobpm:lite", "total > 100")
			require.NoError(t, err)

			_, err = te.Evaluate(ctx, nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), "engine registry")

			_, err = te.Result()
			require.Error(t, err)
		})

	t.Run("BodyHolder capability: text yes, functor no",
		func(t *testing.T) {
			te, err := data.NewTextExpression("gobpm:lite", "x")
			require.NoError(t, err)

			var fe data.FormalExpression = te
			_, ok := fe.(data.BodyHolder)
			require.True(t, ok)

			ge, err := goexpr.New(nil,
				data.MustItemDefinition(values.NewVariable(false)),
				func(context.Context, data.Source) (data.Value, error) {
					return values.NewVariable(true), nil
				})
			require.NoError(t, err)

			var gfe data.FormalExpression = ge
			_, ok = gfe.(data.BodyHolder)
			require.False(t, ok,
				"the functor kind must not claim a text body")
		})
}
