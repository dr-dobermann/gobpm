package artifacts_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/artifacts"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// TestNewTextAnnotation covers SRD-092 T-2: both attributes are optional in
// the standard, and the empty format takes the spec's default.
func TestNewTextAnnotation(t *testing.T) {
	t.Run("text and format are carried", func(t *testing.T) {
		ta, err := artifacts.NewTextAnnotation("Careful", "text/xhtml",
			foundation.WithID("note"))
		require.NoError(t, err)
		require.Equal(t, "note", ta.ID())
		require.Equal(t, "Careful", ta.Text())
		require.Equal(t, "text/xhtml", ta.TextFormat())
	})

	t.Run("an empty text is accepted", func(t *testing.T) {
		ta, err := artifacts.NewTextAnnotation("", "")
		require.NoError(t, err)
		require.Empty(t, ta.Text())
	})

	t.Run("an empty format defaults to text/plain", func(t *testing.T) {
		ta, err := artifacts.NewTextAnnotation("Careful", "")
		require.NoError(t, err)
		require.Equal(t, foundation.PlainText, ta.TextFormat())
	})

	t.Run("an invalid base option is propagated", func(t *testing.T) {
		ta, err := artifacts.NewTextAnnotation("Careful", "",
			foundation.WithID(""))
		require.Error(t, err)
		require.Nil(t, ta)
		require.Contains(t, err.Error(), "empty id isn't allowed")
	})
}

// TestMustTextAnnotation covers the Must* twin's both branches (SRD-092 T-3).
func TestMustTextAnnotation(t *testing.T) {
	t.Run("returns on success", func(t *testing.T) {
		ta := artifacts.MustTextAnnotation("Careful", "")
		require.NotNil(t, ta)
		require.Equal(t, "Careful", ta.Text())
	})

	t.Run("panics on error", func(t *testing.T) {
		require.Panics(t, func() {
			artifacts.MustTextAnnotation("Careful", "",
				foundation.WithID(""))
		})
	})
}
