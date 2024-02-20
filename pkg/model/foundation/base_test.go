package foundation_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestDocumentation(t *testing.T) {
	t.Run("empty doc",
		func(t *testing.T) {
			d := foundation.NewDoc("", "")

			require.Equal(t, "", d.Text())
			require.Equal(t, "text/plain", d.Format())
		})

	t.Run("configured doc",
		func(t *testing.T) {
			d := foundation.NewDoc("test", "text/rtf")

			require.Equal(t, "test", d.Text())
			require.Equal(t, "text/rtf", d.Format())
		})
}

func TestBaseElement(t *testing.T) {
	t.Run("no options",
		func(t *testing.T) {
			be, err := foundation.NewBaseElement()

			require.NoError(t, err)
			require.NotEmpty(t, be.Id())
			require.Empty(t, be.Docs())
		})

	t.Run("with_id",
		func(t *testing.T) {
			be := foundation.MustBaseElement(foundation.WithId("test_id"))

			require.Equal(t, "test_id", be.Id())
		})

	t.Run("with_docs",
		func(t *testing.T) {
			be := foundation.MustBaseElement(foundation.WithDocs(
				foundation.NewDoc("test_doc1", ""),
				foundation.NewDoc("test_doc2", "test/plain"),
			))

			require.NotEmpty(t, be.Id())

			require.Equal(t, 2, len(be.Docs()))
			require.Equal(t, "test_doc1", be.Docs()[0].Text())
			require.Equal(t, "test_doc2", be.Docs()[1].Text())
		})
}
