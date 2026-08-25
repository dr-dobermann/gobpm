package artifacts_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/artifacts"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestAssociationDirection(t *testing.T) {
	t.Run("association direction constants", func(t *testing.T) {
		require.Equal(t, artifacts.AssociationDirection("None"), artifacts.None)
		require.Equal(t, artifacts.AssociationDirection("One"), artifacts.One)
		require.Equal(t, artifacts.AssociationDirection("Both"), artifacts.Both)
	})
}

// TestNewAssociation covers SRD-092 T-1: the constructor validates every
// parameter with a self-identifying error.
func TestNewAssociation(t *testing.T) {
	src := foundation.MustBaseElement(foundation.WithID("src"))
	trg := foundation.MustBaseElement(foundation.WithID("trg"))

	t.Run("valid ends and direction are accepted", func(t *testing.T) {
		a, err := artifacts.NewAssociation(src, trg, artifacts.One,
			foundation.WithID("a1"))
		require.NoError(t, err)
		require.Equal(t, "a1", a.ID())
		require.Same(t, src, a.Source())
		require.Same(t, trg, a.Target())
		require.Equal(t, artifacts.One, a.Direction())
	})

	t.Run("an empty direction defaults to None", func(t *testing.T) {
		a, err := artifacts.NewAssociation(src, trg, "")
		require.NoError(t, err)
		require.Equal(t, artifacts.None, a.Direction())
	})

	t.Run("a nil source is refused", func(t *testing.T) {
		a, err := artifacts.NewAssociation(nil, trg, artifacts.None)
		require.Error(t, err)
		require.Nil(t, a)
		require.Contains(t, err.Error(), "nil source")
	})

	t.Run("a nil target is refused", func(t *testing.T) {
		a, err := artifacts.NewAssociation(src, nil, artifacts.None)
		require.Error(t, err)
		require.Nil(t, a)
		require.Contains(t, err.Error(), "nil target")
	})

	t.Run("an unknown direction is refused", func(t *testing.T) {
		a, err := artifacts.NewAssociation(src, trg, "Sideways")
		require.Error(t, err)
		require.Nil(t, a)
		require.Contains(t, err.Error(), "Sideways")
	})

	t.Run("an invalid base option is propagated", func(t *testing.T) {
		a, err := artifacts.NewAssociation(src, trg, artifacts.None,
			foundation.WithID(""))
		require.Error(t, err)
		require.Nil(t, a)
		require.Contains(t, err.Error(), "empty id isn't allowed")
	})
}

// TestMustAssociation covers the Must* twin's both branches (SRD-092 T-3).
func TestMustAssociation(t *testing.T) {
	src := foundation.MustBaseElement(foundation.WithID("src"))
	trg := foundation.MustBaseElement(foundation.WithID("trg"))

	t.Run("returns on success", func(t *testing.T) {
		a := artifacts.MustAssociation(src, trg, artifacts.Both)
		require.NotNil(t, a)
		require.Equal(t, artifacts.Both, a.Direction())
	})

	t.Run("panics on error", func(t *testing.T) {
		require.Panics(t, func() {
			artifacts.MustAssociation(nil, trg, artifacts.None)
		})
	})
}
