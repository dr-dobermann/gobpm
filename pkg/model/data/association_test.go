package data_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/mock"
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

			ctx := context.Background()
			v, err := a.Value(ctx)
			require.NoError(t, err)
			require.Equal(t, 100, v.Structure().Get(ctx))

			// update non-existed association source
			err = a.UpdateSource(
				ctx,
				data.MustItemDefinition(
					values.NewVariable(42),
					foundation.WithID("invalid source")),
				data.Recalculate)
			require.Error(t, err)

			// update association source
			err = a.UpdateSource(
				ctx,
				data.MustItemDefinition(
					values.NewVariable(42),
					foundation.WithID("source")),
				data.Recalculate)
			require.NoError(t, err)

			require.True(t, a.IsReady())

			v, err = a.Value(ctx)

			require.NoError(t, err)
			require.Equal(t, 42, v.Structure().Get(ctx))

			// with transformation
			mfe := mockdata.NewMockFormalExpression(t)
			mfe.EXPECT().Evaluate(mock.Anything, mock.Anything).
				RunAndReturn(
					func(ctx context.Context, src data.Source) (data.Value, error) {
						v, err := src.Find(ctx, "value")
						if err != nil {
							return nil,
								fmt.Errorf("couldn't get value")
						}

						res, ok := v.Value().Get(ctx).(int)
						if !ok {
							return nil,
								fmt.Errorf("value conversion to int failed")
						}

						m, err := src.Find(ctx, "multiplicator")
						if err != nil {
							return nil,
								fmt.Errorf("couldn't get multiplicator")
						}

						mul, ok := m.Value().Get(ctx).(int)
						if !ok {
							return nil,
								fmt.Errorf("multiplicator conversion to int failed")
						}

						return values.NewVariable(res * mul), nil
					})

			a, err = data.NewAssociation(
				trgIAE,
				data.WithTransformation(mfe),
				data.WithSource(
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(100),
							foundation.WithID("value")),
						data.UndefinedDataState)),
				data.WithSource(
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(2),
							foundation.WithID("multiplicator")),
						data.ReadyDataState)),
				foundation.WithID("association with transformation"))
			require.NoError(t, err)

			require.False(t, a.IsReady())
			_, err = a.Value(ctx)
			require.Error(t, err)

			// update association source
			err = a.UpdateSource(
				ctx,
				data.MustItemDefinition(
					values.NewVariable(42),
					foundation.WithID("value")),
				data.Recalculate)
			require.NoError(t, err)

			trg, err := a.Value(context.Background())
			require.NoError(t, err)
			require.Equal(t, 84, trg.Structure().Get(ctx))
		})
}
