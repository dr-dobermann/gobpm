package data_test

import (
	"context"
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
			foundation.WithId("output")),
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
						foundation.WithId("src 1"))),
				data.WithSource(
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable("one hundred")),
						data.ReadyDataState,
						foundation.WithId("src 2"))))
		})

	t.Run("normal",
		func(t *testing.T) {
			// mfe := mockdata.NewMockFormalExpression(t)

			// no transformation
			a, err := data.NewAssociation(
				trgIAE,
				data.WithSource(
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(100),
							foundation.WithId("source")),
						data.ReadyDataState)))
			require.NoError(t, err)

			require.False(t, a.IsReady())

			require.Equal(t, "output", a.TargetItemDefId())

			srcL := a.SourcesIds()
			require.Equal(t, 1, len(srcL))
			require.Contains(t, srcL, "source")

			require.False(t, a.HasSourceId("invalid src id"))

			require.True(t, a.HasSourceId("source"))

			v, err := a.Value(context.Background())
			require.NoError(t, err)
			require.Equal(t, 100, v.Structure().Get())

			// update non-existed association source
			err = a.UpdateSource(
				context.Background(),
				data.MustItemDefinition(
					values.NewVariable(42),
					foundation.WithId("invalid source")))
			require.Error(t, err)

			// update association source
			err = a.UpdateSource(
				context.Background(),
				data.MustItemDefinition(
					values.NewVariable(42),
					foundation.WithId("source")))
			require.NoError(t, err)

			require.False(t, a.IsReady())

			v, err = a.Value(context.Background())

			require.NoError(t, err)
			require.Equal(t, 42, v.Structure().Get())
		})
}
