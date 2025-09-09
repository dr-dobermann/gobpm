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

func TestAssociation(t *testing.T) {
	t.Run("association struct", func(t *testing.T) {
		// Create source and target elements
		source := foundation.MustBaseElement(foundation.WithID("source-id"))
		target := foundation.MustBaseElement(foundation.WithID("target-id"))

		// Test Association struct creation
		assoc := artifacts.Association{
			BaseElement: *foundation.MustBaseElement(foundation.WithID("assoc-id")),
			Direction:   artifacts.One,
			Source:      source,
			Target:      target,
		}

		require.Equal(t, "assoc-id", assoc.ID())
		require.Equal(t, artifacts.One, assoc.Direction)
		require.Equal(t, source, assoc.Source)
		require.Equal(t, target, assoc.Target)
	})

	t.Run("different directions", func(t *testing.T) {
		source := foundation.MustBaseElement()
		target := foundation.MustBaseElement()

		// Test None direction
		assocNone := artifacts.Association{
			BaseElement: *foundation.MustBaseElement(),
			Direction:   artifacts.None,
			Source:      source,
			Target:      target,
		}
		require.Equal(t, artifacts.None, assocNone.Direction)

		// Test Both direction
		assocBoth := artifacts.Association{
			BaseElement: *foundation.MustBaseElement(),
			Direction:   artifacts.Both,
			Source:      source,
			Target:      target,
		}
		require.Equal(t, artifacts.Both, assocBoth.Direction)
	})

	t.Run("nil source and target", func(t *testing.T) {
		// Test with nil source and target
		assoc := artifacts.Association{
			BaseElement: *foundation.MustBaseElement(),
			Direction:   artifacts.One,
			Source:      nil,
			Target:      nil,
		}

		require.Nil(t, assoc.Source)
		require.Nil(t, assoc.Target)
		require.Equal(t, artifacts.One, assoc.Direction)
	})
}
