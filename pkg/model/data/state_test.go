package data_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestDataState(t *testing.T) {
	t.Run("empty name",
		func(t *testing.T) {
			_, err := data.NewDataState("   ")
			require.Error(t, err)

			require.Panics(t,
				func() {
					_ = data.MustDataState("")
				})
		})

	t.Run("name and id",
		func(t *testing.T) {
			ds, err := data.NewDataState(
				"test_ds",
				foundation.WithId("test_ds_id"))

			require.NoError(t, err)
			require.NotEmpty(t, ds)

			require.Equal(t, "test_ds", ds.Name())
			require.Equal(t, "test_ds_id", ds.Id())
		})

	t.Run("name and invalid option",
		func(t *testing.T) {
			ds, err := data.NewDataState(
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
			t.Log("defalut DataStates created",
				data.UnavailableDataState.Name())
		})
}
