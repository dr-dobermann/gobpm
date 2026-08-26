package artifacts_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/artifacts"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// The compile-time half of SRD-092 T-4: the three carried kinds satisfy the
// closed Artifact interface.
var (
	_ artifacts.Artifact = (*artifacts.Association)(nil)
	_ artifacts.Artifact = (*artifacts.TextAnnotation)(nil)
	_ artifacts.Artifact = (*artifacts.Group)(nil)
)

// TestArtifactInterface keeps T-4's claim visible in the test run: every
// carried kind is usable through the interface.
func TestArtifactInterface(t *testing.T) {
	src := foundation.MustBaseElement(foundation.WithID("src"))
	trg := foundation.MustBaseElement(foundation.WithID("trg"))

	arts := []artifacts.Artifact{
		artifacts.MustAssociation(src, trg, artifacts.None),
		artifacts.MustTextAnnotation("Careful", ""),
		artifacts.MustGroup("critical"),
	}

	for _, a := range arts {
		require.NotEmpty(t, a.ID())
	}
}

// TestAppend covers the collection invariant both containers delegate to
// (SRD-092 FR-5): nil entries and duplicate ids are refused.
func TestAppend(t *testing.T) {
	a1 := artifacts.MustTextAnnotation("one", "", foundation.WithID("a1"))
	a2 := artifacts.MustTextAnnotation("two", "", foundation.WithID("a2"))

	t.Run("appends in order, extending an existing collection",
		func(t *testing.T) {
			aa, err := artifacts.Append(nil, a1, a2)
			require.NoError(t, err)
			require.Len(t, aa, 2)
			require.Equal(t, "a1", aa[0].ID())
			require.Equal(t, "a2", aa[1].ID())

			aa, err = artifacts.Append(aa, artifacts.MustGroup("g",
				foundation.WithID("a3")))
			require.NoError(t, err)
			require.Len(t, aa, 3)
			require.Equal(t, "a3", aa[2].ID())
		})

	t.Run("a nil artifact is refused", func(t *testing.T) {
		_, err := artifacts.Append(nil, a1, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "nil artifact")
	})

	t.Run("an id duplicating the existing collection is refused",
		func(t *testing.T) {
			_, err := artifacts.Append([]artifacts.Artifact{a1},
				artifacts.MustTextAnnotation("x", "", foundation.WithID("a1")))
			require.Error(t, err)
			require.Contains(t, err.Error(), "duplicate artifact id")
		})

	t.Run("an id duplicated within the added batch is refused",
		func(t *testing.T) {
			_, err := artifacts.Append(nil, a1,
				artifacts.MustTextAnnotation("x", "", foundation.WithID("a1")))
			require.Error(t, err)
			require.Contains(t, err.Error(), "duplicate artifact id")
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

// TestNewGroupBaseElementError covers NewGroup's base-element build-failure
// branch: an invalid base option (empty explicit id) fails NewBaseElement.
func TestNewGroupBaseElementError(t *testing.T) {
	_, err := artifacts.NewGroup("cat", foundation.WithID(""))
	require.Error(t, err)
}

// TestMustGroupPanics covers MustGroup's panic branch on a NewGroup failure.
func TestMustGroupPanics(t *testing.T) {
	require.Panics(t, func() {
		artifacts.MustGroup("cat", foundation.WithID(""))
	})
}
