package artifacts_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/artifacts"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestArtifact(t *testing.T) {
	t.Run("new artifact success", func(t *testing.T) {
		art, err := artifacts.NewArtifact()
		require.NoError(t, err)
		require.NotNil(t, art)
		require.NotEmpty(t, art.ID())
	})

	t.Run("new artifact with custom id", func(t *testing.T) {
		customID := "custom-artifact-id"
		art, err := artifacts.NewArtifact(foundation.WithID(customID))
		require.NoError(t, err)
		require.NotNil(t, art)
		require.Equal(t, customID, art.ID())
	})

	t.Run("must artifact success", func(t *testing.T) {
		art := artifacts.MustArtifact()
		require.NotNil(t, art)
		require.NotEmpty(t, art.ID())
	})

	t.Run("must artifact with custom id", func(t *testing.T) {
		customID := "must-artifact-id"
		art := artifacts.MustArtifact(foundation.WithID(customID))
		require.NotNil(t, art)
		require.Equal(t, customID, art.ID())
	})

	t.Run("new artifact with invalid option", func(t *testing.T) {
		// Test error path: empty ID is not allowed
		art, err := artifacts.NewArtifact(foundation.WithID(""))
		require.Error(t, err)
		require.Nil(t, art)
		require.Contains(t, err.Error(), "empty id isn't allowed")
	})

	t.Run("must artifact panics on error", func(t *testing.T) {
		// Test panic path in MustArtifact
		require.Panics(t, func() {
			artifacts.MustArtifact(foundation.WithID(""))
		})
	})
}

func TestGroup(t *testing.T) {
	t.Run("new group success", func(t *testing.T) {
		groupName := "test-group"
		group, err := artifacts.NewGroup(groupName)
		require.NoError(t, err)
		require.NotNil(t, group)
		require.NotEmpty(t, group.ID())
		require.NotNil(t, group.CategoryValue)
		require.Equal(t, groupName, group.CategoryValue.Value)
		require.Equal(t, group.ID(), group.CategoryValue.ID())
	})

	t.Run("new group with custom id", func(t *testing.T) {
		groupName := "custom-group"
		customID := "custom-group-id"
		group, err := artifacts.NewGroup(groupName, foundation.WithID(customID))
		require.NoError(t, err)
		require.NotNil(t, group)
		require.Equal(t, customID, group.ID())
		require.Equal(t, groupName, group.CategoryValue.Value)
	})

	t.Run("must group success", func(t *testing.T) {
		groupName := "must-group"
		group := artifacts.MustGroup(groupName)
		require.NotNil(t, group)
		require.NotEmpty(t, group.ID())
		require.NotNil(t, group.CategoryValue)
		require.Equal(t, groupName, group.CategoryValue.Value)
	})

	t.Run("must group with custom id", func(t *testing.T) {
		groupName := "must-custom-group"
		customID := "must-custom-group-id"
		group := artifacts.MustGroup(groupName, foundation.WithID(customID))
		require.NotNil(t, group)
		require.Equal(t, customID, group.ID())
		require.Equal(t, groupName, group.CategoryValue.Value)
	})
}
