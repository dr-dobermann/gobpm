package data_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

func TestProperty(t *testing.T) {
	t.Run("errors",
		func(t *testing.T) {
			// empty name
			_, err := data.NewProperty("", nil, nil)
			require.Error(t, err)
			require.Panics(t,
				func() {
					_ = data.MustProperty("", nil, nil)
				})

			_, err = data.NewProp("", nil)
			require.Error(t, err)

			// empty parameters
			_, err = data.NewProperty("empty item", nil, data.ReadyDataState)
			require.Error(t, err)

			_, err = data.NewProp("empty iae", nil)
			require.Error(t, err)

			// invalid option
			_, err = data.NewProperty(
				"invalid option",
				data.MustItemDefinition(nil),
				data.UnavailableDataState,
				options.WithName("extra name"))
			require.Error(t, err)
		})
}
