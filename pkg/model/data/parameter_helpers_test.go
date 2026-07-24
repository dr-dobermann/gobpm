package data_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// TestReadyParameter covers FIX-026 §3.2: the shared Ready-datum builders
// fail with an error on bad input — never a panic — so every runtime commit
// path that uses them inherits the guarantee.
func TestReadyParameter(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	item := data.MustItemDefinition(values.NewVariable(42))

	t.Run("valid input builds a Ready parameter",
		func(t *testing.T) {
			p, err := data.ReadyParameter("answer", item)
			require.NoError(t, err)
			require.Equal(t, "answer", p.Name())
			require.Equal(t, 42, p.Value().Get(context.Background()))
		})

	t.Run("nil item rejected",
		func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := data.ReadyParameter("answer", nil)
				require.Error(t, err)
			})
		})

	t.Run("empty name rejected",
		func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := data.ReadyParameter("", item)
				require.Error(t, err)
			})
		})
}

// TestReadyValueParameter covers the value-first twin, including item
// options.
func TestReadyValueParameter(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("valid value builds a Ready parameter with the item id",
		func(t *testing.T) {
			p, err := data.ReadyValueParameter("total",
				values.NewVariable(100), foundation.WithID("total"))
			require.NoError(t, err)
			require.Equal(t, "total", p.Name())
			require.Equal(t, "total", p.ItemDefinition().ID())
		})

	t.Run("invalid item option rejected",
		func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := data.ReadyValueParameter("total",
					values.NewVariable(100), foundation.WithID(""))
				require.Error(t, err)
			})
		})

	t.Run("empty name rejected",
		func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := data.ReadyValueParameter("",
					values.NewVariable(100))
				require.Error(t, err)
			})
		})
}
