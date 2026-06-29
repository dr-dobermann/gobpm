package goexpr_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// TestGoexprNilResult pins FIX-010 §3.2.4: a user GExpFunc that returns
// (nil, nil) must yield a classified error rather than panic on res.Get.
func TestGoexprNilResult(t *testing.T) {
	res := data.MustItemDefinition(values.NewVariable(false))

	ge, err := goexpr.New(nil, res,
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return nil, nil
		})
	require.NoError(t, err)

	require.NotPanics(t, func() {
		_, err = ge.Evaluate(context.Background(), nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "nil value")
	})
}
