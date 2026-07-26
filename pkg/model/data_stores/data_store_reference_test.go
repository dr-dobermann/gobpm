package datastores_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	datastores "github.com/dr-dobermann/gobpm/pkg/model/data_stores"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestDataStoreReferenceModel(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	idef := func() *data.ItemDefinition {
		return data.MustItemDefinition(
			values.NewVariable(0), foundation.WithID("total"))
	}

	t.Run("construction and accessors", func(t *testing.T) {
		r, err := datastores.New(
			"order-total", "orders", idef(), data.ReadyDataState)
		require.NoError(t, err)
		require.Equal(t, "order-total", r.Name())
		require.Equal(t, "orders", r.DataStoreRef())
		require.Equal(t, flow.DataStoreReferenceElement, r.EType())
		require.NotEmpty(t, r.ID())
		require.Empty(t, r.Docs())
	})

	t.Run("empty name rejected", func(t *testing.T) {
		_, err := datastores.New("", "orders", idef(), data.ReadyDataState)
		require.Error(t, err)
	})

	t.Run("reserved char in name rejected", func(t *testing.T) {
		_, err := datastores.New("bad.name", "orders", idef(), data.ReadyDataState)
		require.Error(t, err)
	})

	t.Run("empty dataStoreRef rejected", func(t *testing.T) {
		_, err := datastores.New("x", "", idef(), data.ReadyDataState)
		require.Error(t, err)
	})

	t.Run("nil item definition rejected", func(t *testing.T) {
		_, err := datastores.New("x", "orders", nil, data.ReadyDataState)
		require.Error(t, err)
	})
}
