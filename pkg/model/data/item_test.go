package data_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestItemDefinition(t *testing.T) {
	t.Run("new_with_defaults",
		func(t *testing.T) {
			id, err := data.NewItemDefinition(nil)

			require.NoError(t, err)
			require.Equal(t, data.Information, id.Kind())
			require.Equal(t, false, id.IsCollection())
			require.Equal(t, nil, id.Structure())
			require.NotEqual(t, "", id.Id())
		})

	t.Run("new_with_all_settings",
		func(t *testing.T) {
			id, err := data.NewItemDefinition(nil,
				foundation.WithId("test_id"),
				data.WithKind(data.Physical),
				foundation.WithDocs(
					foundation.NewDoc("doc1", ""),
					foundation.NewDoc("doc2", ""),
				),
				data.WithImport(&foundation.Import{
					Type:      "test",
					Location:  "test/url",
					Namespace: "gobpm",
				}))

			require.NoError(t, err)
			require.Equal(t, data.Physical, id.Kind())
			require.Equal(t, false, id.IsCollection())
			require.Equal(t, nil, id.Structure())
			require.Equal(t, "test_id", id.Id())
			require.Equal(t, 2, len(id.Docs()))
			require.Equal(t, "test", id.Import().Type)
		})
}
