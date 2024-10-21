package data_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestItemDefinition(t *testing.T) {
	t.Run("new_with_defaults",
		func(t *testing.T) {
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
	id, err := data.NewItemDefinition(values.NewVariable[int](42))
	require.NoError(t, err)

	ds, err := data.NewDataState("test ds")
	require.NoError(t, err)
	t.Run("empty parameters",
		func(t *testing.T) {
			iae, err := data.NewItemAwareElement(nil, nil)
			require.Error(t, err)
			require.Empty(t, iae)

			iae, err = data.NewItemAwareElement(id, nil)
			require.Error(t, err)
			require.Empty(t, iae)
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
			// invalid options
		})
}
