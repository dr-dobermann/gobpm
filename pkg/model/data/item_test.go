package data_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

func TestItemDefinition(t *testing.T) {
	t.Run("item_kind",
		func(t *testing.T) {
			invlaid_kind := data.ItemKind("invld_kind")
			require.Error(t, invlaid_kind.Validate())
		})

	t.Run("new_with_defaults",
		func(t *testing.T) {
			_, err := data.NewItemDefinition(nil, options.WithName("invlaid iDef"))
			require.Error(t, err)

			id, err := data.NewItemDefinition(nil)

			require.NoError(t, err)
			require.Equal(t, data.InformationKind, id.Kind())
			require.Equal(t, false, id.IsCollection())
			require.Equal(t, nil, id.Structure())
			require.NotEqual(t, "", id.Id())
		})

	t.Run("new_with_all_settings",
		func(t *testing.T) {
			id, err := data.NewItemDefinition(nil,
				foundation.WithId("test_id"),
				data.WithKind(data.PhysicalKind),
				foundation.WithDoc("doc1", ""),
				foundation.WithDoc("doc2", ""),
				data.WithImport(&foundation.Import{
					Type:      "test",
					Location:  "test/url",
					Namespace: "gobpm",
				}))

			require.NoError(t, err)
			require.Equal(t, data.PhysicalKind, id.Kind())
			require.Equal(t, false, id.IsCollection())
			require.Equal(t, nil, id.Structure())
			require.Equal(t, "test_id", id.Id())
			require.Equal(t, 2, len(id.Docs()))
			require.Equal(t, "test", id.Import().Type)
		})
}

func TestItemAwareElement(t *testing.T) {
	data.CreateDefaultStates()

	id, err := data.NewItemDefinition(values.NewVariable(42))
	require.NoError(t, err)

	ds, err := data.NewDataState("test ds")
	require.NoError(t, err)
	t.Run("invalid parameters",
		func(t *testing.T) {
			_, err := data.NewItemAwareElement(nil, nil)
			require.Error(t, err)

			_, err = data.NewItemAwareElement(id, nil)
			require.NoError(t, err)
		})

	t.Run("itemDefinition, State and Id",
		func(t *testing.T) {
			iae, err := data.NewItemAwareElement(
				id, ds, foundation.WithId("test iae"))
			require.NoError(t, err)
			require.NotEmpty(t, iae)

			require.Equal(t, "test ds", iae.State().Name())
			require.Equal(t, "test iae", iae.Id())
		})

	t.Run("options-like constructor",
		func(t *testing.T) {
			// no parameters
			_, err = data.NewIAE()
			require.Error(t, err)

			// empty parameters
			_, err = data.NewIAE(nil)
			require.Error(t, err)

			// invalid options
			_, err = data.NewIAE(options.WithName("invalid option"))
			require.Error(t, err)

			// invalid state
			_, err = data.NewIAE(data.WithIDefinition(nil), data.WithState(data.ReadyDataState))
			require.Error(t, err)

			// normal with empty IDef
			uIAE, err := data.NewIAE(data.WithIDefinition(nil),
				foundation.WithId("empty_value_iae"))
			require.NoError(t, err)
			require.NotNil(t, uIAE.Subject())
			require.NotNil(t, uIAE.ItemDefinition())
			require.Nil(t, uIAE.Value())
			require.False(t, uIAE.IsCollection())
			require.Equal(t, data.UndefinedDataState.Name(), uIAE.State().Name())
			require.Error(t, uIAE.UpdateState(data.ReadyDataState))

			// normal with IDef and Ready state
			rIAE, err := data.NewIAE(
				data.WithIDef(id),
				data.WithState(data.ReadyDataState))
			require.NoError(t, err)
			require.Equal(t, data.ReadyDataState.Name(), rIAE.State().Name())
			require.NoError(t, rIAE.UpdateState(data.UnavailableDataState))
			require.False(t, rIAE.IsCollection())
			require.NotNil(t, rIAE.ItemDefinition())
			require.Equal(t, id.Structure().Get(), rIAE.Value().Get())
		})
}
