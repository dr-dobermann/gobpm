package goexpr_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// alwaysTrue is a trivial bool GExpFunc for option tests.
func alwaysTrue(_ context.Context, _ data.Source) (data.Value, error) {
	return values.NewVariable(true), nil
}

func TestWithDependencies(t *testing.T) {
	data.CreateDefaultStates()

	boolRes := func() *data.ItemDefinition {
		return data.MustItemDefinition(values.NewVariable(false))
	}

	t.Run("declared list surfaces via Dependencies", func(t *testing.T) {
		ge, err := goexpr.New(nil, boolRes(), alwaysTrue,
			goexpr.WithDependencies("order", "order.items[0].price"))
		require.NoError(t, err)
		require.Equal(t,
			[]string{"order", "order.items[0].price"}, ge.Dependencies())
	})

	t.Run("plain construction declares nothing", func(t *testing.T) {
		ge, err := goexpr.New(nil, boolRes(), alwaysTrue)
		require.NoError(t, err)
		require.Nil(t, ge.Dependencies())

		// the capability is present on every GExpression; nil means
		// "may read anything".
		var dl data.DependencyLister = ge
		require.Nil(t, dl.Dependencies())
	})

	t.Run("empty call rejected", func(t *testing.T) {
		_, err := goexpr.New(nil, boolRes(), alwaysTrue,
			goexpr.WithDependencies())
		require.Error(t, err)
		require.Contains(t, err.Error(), "WithDependencies")
	})

	t.Run("empty path rejected", func(t *testing.T) {
		_, err := goexpr.New(nil, boolRes(), alwaysTrue,
			goexpr.WithDependencies("order", ""))
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty path")
	})

	t.Run("malformed path rejected", func(t *testing.T) {
		_, err := goexpr.New(nil, boolRes(), alwaysTrue,
			goexpr.WithDependencies("a..b"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid path")
	})

	t.Run("non-goexpr options still forwarded", func(t *testing.T) {
		ge, err := goexpr.New(nil, boolRes(), alwaysTrue,
			foundation.WithID("dep-expr"),
			goexpr.WithDependencies("x"))
		require.NoError(t, err)
		require.Equal(t, "dep-expr", ge.ID())
		require.Equal(t, []string{"x"}, ge.Dependencies())
	})
}
