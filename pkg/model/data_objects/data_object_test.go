package dataobjects_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/generated/mockflow"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const name = "test DO"

func TestNew(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("errors",
		func(t *testing.T) {
			// empty name
			_, err := dataobjects.New("", nil, nil)
			require.Error(t, err)

			// no IAE
			_, err = dataobjects.New("no IAE", nil, nil)
			require.Error(t, err)

			// invalid options
			_, err = dataobjects.New(
				"no IAE",
				data.MustItemDefinition(
					values.NewVariable(10)),
				data.ReadyDataState,
				options.WithName("invalid option"))
			require.Error(t, err)
		})

	t.Run("normal",
		func(t *testing.T) {
			do, err := dataobjects.New(
				name,
				data.MustItemDefinition(
					values.NewVariable(100),
					foundation.WithId("input")),
				data.ReadyDataState,
				foundation.WithId("normalDO"))
			require.NoError(t, err)

			require.Equal(t, name, do.Name())
			require.Equal(t, flow.DataObjectElement, do.Type())
			require.Empty(t, do.Docs())
			require.Equal(t, "normalDO", do.Id())

			// empty source error
			err = do.AssociateSource(nil, nil, nil)
			require.Error(t, err)
		})
}

func TestSourceAssociations(t *testing.T) {
	t.Run("no source",
		func(t *testing.T) {
			do, err := dataobjects.New(
				name,
				data.MustItemDefinition(
					values.NewVariable(100),
					foundation.WithId("targetIDef")),
				data.ReadyDataState)
			require.NoError(t, err)

			aSrc := mockflow.NewMockAssociationSource(t)
			aSrc.EXPECT().
				Outputs().
				Return([]*data.ItemAwareElement{
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(42),
							foundation.WithId("wrong_input")),
						data.ReadyDataState),
				})
			err = do.AssociateSource(aSrc, []string{"input"}, nil)
			require.Error(t, err)
		})

	t.Run("bind source error",
		func(t *testing.T) {
			do, err := dataobjects.New(
				name,
				data.MustItemDefinition(
					values.NewVariable(100),
					foundation.WithId("targetIDef")),
				data.ReadyDataState)
			require.NoError(t, err)

			aSrc := mockflow.NewMockAssociationSource(t)
			aSrc.EXPECT().
				Name().
				Return("Task 1")
			aSrc.EXPECT().
				Outputs().
				Return([]*data.ItemAwareElement{
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(42),
							foundation.WithId("input")),
						data.ReadyDataState),
				})
			aSrc.EXPECT().
				BindOutgoing(mock.Anything).
				RunAndReturn(
					func(da *data.Association) error {
						return fmt.Errorf("binding failed")
					})
			err = do.AssociateSource(aSrc, []string{"input"}, nil)
			require.Error(t, err)
		})

	t.Run("normal",
		func(t *testing.T) {
			do, err := dataobjects.New(
				name,
				data.MustItemDefinition(
					values.NewVariable(100),
					foundation.WithId("targetIDef")),
				data.ReadyDataState)
			require.NoError(t, err)

			tran := mockdata.NewMockFormalExpression(t)
			tran.EXPECT().
				Evaluate(mock.Anything, mock.Anything).
				RunAndReturn(
					func(
						ctx context.Context,
						ds data.Source,
					) (data.Value, error) {
						x, err := ds.Find(ctx, "input")
						if err != nil {
							return nil,
								fmt.Errorf("couldn't get input value: %w", err)
						}

						return values.NewVariable(x.ItemDefinition().Structure().Get().(int) * 2),
							nil
					})

			aSrc := mockflow.NewMockAssociationSource(t)
			aSrc.EXPECT().
				Outputs().
				Return([]*data.ItemAwareElement{
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(42),
							foundation.WithId("input")),
						data.ReadyDataState),
				})
			aSrc.EXPECT().
				BindOutgoing(mock.Anything).
				RunAndReturn(
					func(da *data.Association) error {
						require.Equal(t, 1, len(da.SourcesIds()))
						require.Contains(t, da.SourcesIds(), "input")
						require.Equal(t, "targetIDef", da.TargetItemDefId())

						val, err := da.Value(context.Background())
						require.NoError(t, err)
						require.Equal(t, 84, val.Structure().Get())

						return nil
					})
			err = do.AssociateSource(aSrc, []string{"input"}, tran)
			require.NoError(t, err)
		})
}

func TestTargetAssociations(t *testing.T) {
	t.Run("no source on target",
		func(t *testing.T) {
			do, err := dataobjects.New(
				name,
				data.MustItemDefinition(
					values.NewVariable(100),
					foundation.WithId("x")),
				data.ReadyDataState)
			require.NoError(t, err)

			aTrg := mockflow.NewMockAssociationTarget(t)
			aTrg.EXPECT().
				Id().
				Return("Task 1").
				Maybe()
			aTrg.EXPECT().
				Name().
				Return("Task 1").
				Maybe()
			aTrg.EXPECT().
				Inputs().
				Return([]*data.ItemAwareElement{
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(42),
							foundation.WithId("y")),
						data.ReadyDataState),
				}).
				Maybe()

			err = do.AssociateTarget(aTrg, nil)
			require.Error(t, err)
		})

	t.Run("bind failing",
		func(t *testing.T) {
			do, err := dataobjects.New(
				name,
				data.MustItemDefinition(
					values.NewVariable(100),
					foundation.WithId("x")),
				data.ReadyDataState)
			require.NoError(t, err)

			aTrg := mockflow.NewMockAssociationTarget(t)
			aTrg.EXPECT().
				Id().
				Return("Task 1").
				Maybe()
			aTrg.EXPECT().
				Name().
				Return("Task 1").
				Maybe()
			aTrg.EXPECT().
				Inputs().
				Return([]*data.ItemAwareElement{
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(42),
							foundation.WithId("x")),
						data.ReadyDataState),
				}).
				Maybe()
			aTrg.EXPECT().
				BindIncoming(mock.Anything).
				RunAndReturn(
					func(da *data.Association) error {
						return fmt.Errorf("binding failed")
					}).
				Maybe()

			err = do.AssociateTarget(aTrg, nil)
			require.Error(t, err)
		})

	t.Run("normal",
		func(t *testing.T) {
			do, err := dataobjects.New(
				name,
				data.MustItemDefinition(
					values.NewVariable(100),
					foundation.WithId("x")),
				data.ReadyDataState)
			require.NoError(t, err)

			tran := mockdata.NewMockFormalExpression(t)
			tran.EXPECT().
				Evaluate(mock.Anything, mock.Anything).
				RunAndReturn(
					func(
						ctx context.Context,
						ds data.Source,
					) (data.Value, error) {
						x, err := ds.Find(ctx, "x")
						if err != nil {
							return nil,
								fmt.Errorf("couldn't get x value: %w", err)
						}

						return values.NewVariable(x.ItemDefinition().Structure().Get().(int) * 2),
							nil
					})

			aTrg := mockflow.NewMockAssociationTarget(t)
			aTrg.EXPECT().
				Id().
				Return("Task 1").
				Maybe()
			aTrg.EXPECT().
				Name().
				Return("Task 1").
				Maybe()
			aTrg.EXPECT().
				Inputs().
				Return([]*data.ItemAwareElement{
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(42),
							foundation.WithId("x")),
						data.ReadyDataState),
				}).
				Maybe()
			aTrg.EXPECT().
				BindIncoming(mock.Anything).
				RunAndReturn(
					func(da *data.Association) error {
						require.NotNil(t, da)
						require.Equal(t, "x", da.TargetItemDefId())
						require.Equal(t, 1, len(da.SourcesIds()))
						require.Contains(t, da.SourcesIds(), "x")

						val, err := da.Value(context.Background())
						require.NoError(t, err)

						require.Equal(t, 200, val.Structure().Get())
						return nil
					}).
				Maybe()

			err = do.AssociateTarget(aTrg, tran)
			require.NoError(t, err)

			// duplicate target
			err = do.AssociateTarget(aTrg, nil)
			require.Error(t, err)

			// empty target node
			err = do.AssociateTarget(nil, nil)
			require.Error(t, err)
		})
}

func TestUpdate(t *testing.T) {
	t.Run("normal",
		func(t *testing.T) {
			do, err := dataobjects.New(
				name,
				data.MustItemDefinition(
					values.NewVariable(100),
					foundation.WithId("x")),
				data.ReadyDataState)
			require.NoError(t, err)

			tran := mockdata.NewMockFormalExpression(t)
			tran.EXPECT().
				Evaluate(mock.Anything, mock.Anything).
				RunAndReturn(
					func(
						ctx context.Context,
						ds data.Source,
					) (data.Value, error) {
						inp, err := ds.Find(ctx, "input")
						if err != nil {
							return nil,
								fmt.Errorf("couldn't get input value: %w", err)
						}

						x, ok := inp.ItemDefinition().Structure().Get().(int)
						if !ok {
							return nil,
								fmt.Errorf("couldn't convert input to int")
						}

						return values.NewVariable(x * 2),
							nil
					})

			aSrc := mockflow.NewMockAssociationSource(t)
			aSrc.EXPECT().
				Outputs().
				Return([]*data.ItemAwareElement{
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(42),
							foundation.WithId("input")),
						data.ReadyDataState),
				})
			aSrc.EXPECT().
				BindOutgoing(mock.Anything).
				RunAndReturn(
					func(da *data.Association) error {
						require.Equal(t, 1, len(da.SourcesIds()))
						require.Contains(t, da.SourcesIds(), "input")
						require.Equal(t, "x", da.TargetItemDefId())

						val, err := da.Value(context.Background())
						require.NoError(t, err)
						require.Equal(t, 84, val.Structure().Get())

						return nil
					})

			aTrg := mockflow.NewMockAssociationTarget(t)
			aTrg.EXPECT().
				Id().
				Return("Task 1").
				Maybe()
			aTrg.EXPECT().
				Name().
				Return("Task 1").
				Maybe()
			aTrg.EXPECT().
				Inputs().
				Return([]*data.ItemAwareElement{
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(42),
							foundation.WithId("x")),
						data.ReadyDataState),
				}).
				Maybe()
			aTrg.EXPECT().
				BindIncoming(mock.Anything).
				RunAndReturn(
					func(da *data.Association) error {
						require.NotNil(t, da)
						require.Equal(t, "x", da.TargetItemDefId())
						require.Equal(t, 1, len(da.SourcesIds()))
						require.Contains(t, da.SourcesIds(), "x")

						val, err := da.Value(context.Background())
						require.NoError(t, err)

						require.Equal(t, 84, val.Structure().Get())
						return nil
					}).
				Maybe()

			err = do.AssociateSource(aSrc, []string{"input"}, tran)
			require.NoError(t, err)

			err = do.AssociateTarget(aTrg, nil)
			require.NoError(t, err)
		})
}
