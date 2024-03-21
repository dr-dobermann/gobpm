package foundation_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

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
			be := foundation.MustBaseElement(
				foundation.WithDoc("test_doc1", ""),
				foundation.WithDoc("test_doc2", "text/rtf"))

			require.NotEmpty(t, be.Id())

			docs := be.Docs()
			require.Equal(t, 2, len(be.Docs()))
			require.Equal(t, "test_doc1", docs[0].Text())
			require.Equal(t, "test_doc2", docs[1].Text())
			require.Equal(t, "text/plain", docs[0].Format())
			require.Equal(t, "text/rtf", docs[1].Format())
		})
}
