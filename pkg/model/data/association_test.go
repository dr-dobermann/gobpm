package data_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestAssociations(t *testing.T) {
	data.CreateDefaultStates()

	// sample ItemAwareElement target
	trgIAE, err := data.NewIAE(
		data.WithIDefinition(
			values.NewVariable(42),
			foundation.WithID("output")),
		data.WithState(data.ReadyDataState))
	require.NoError(t, err)

	t.Run("errors",
		func(t *testing.T) {
			// invalid parameters
			_, err := data.NewAssociation(
				nil)
			require.Error(t, err)

			// no source without transformation
			_, err = data.NewAssociation(trgIAE)
			require.Error(t, err)

			// multiply sources without transformation
			_, err = data.NewAssociation(
				trgIAE,
				data.WithSource(
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(100)),
						data.ReadyDataState,
						foundation.WithID("src 1"))),
				data.WithSource(
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable("one hundred")),
						data.ReadyDataState,
						foundation.WithID("src 2"))))
			require.Error(t, err)
		})

	t.Run("normal",
		func(t *testing.T) {
			// no transformation
			a, err := data.NewAssociation(
				trgIAE,
				data.WithSource(
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(100),
							foundation.WithID("source")),
						data.ReadyDataState)))
			require.NoError(t, err)

			require.False(t, a.IsReady())

			require.Equal(t, "output", a.TargetItemDefID())

			srcL := a.SourcesIDs()
			require.Equal(t, 1, len(srcL))
			require.Contains(t, srcL, "source")

			require.False(t, a.HasSourceID("invalid src id"))

			require.True(t, a.HasSourceID("source"))
		})
}
