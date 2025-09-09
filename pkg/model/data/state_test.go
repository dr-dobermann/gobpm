package data_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestSrcState(t *testing.T) {
	t.Run("empty name",
		func(t *testing.T) {
			_, err := data.NewSrcState("   ")
			require.Error(t, err)

			require.Panics(t,
				func() {
					_ = data.MustSrcState("")
				})
		})

	t.Run("name and id",
		func(t *testing.T) {
			ds, err := data.NewSrcState(
				"test_ds",
				foundation.WithID("test_ds_id"))

			require.NoError(t, err)
			require.NotEmpty(t, ds)

			require.Equal(t, "test_ds", ds.Name())
			require.Equal(t, "test_ds_id", ds.ID())
		})

	t.Run("name and invalid option",
		func(t *testing.T) {
			ds, err := data.NewSrcState(
				"bad ds",
				data.WithKind(data.PhysicalKind))

			require.Error(t, err)
			require.Empty(t, ds)

			t.Log(err)
		})

	t.Run("default states creation",
		func(t *testing.T) {
			err := data.CreateDefaultStates()

			require.NoError(t, err)
			require.NotNil(t, data.UnavailableDataState)
			t.Log("defalut SrcStates created",
				data.UnavailableDataState.Name())
		})
}
