package data_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// TestItemAwareElementName covers the optional scope name a DataObject sets on
// its item-aware element (SRD-063 FR-5): Name() returns the explicit name when
// set, else the ItemDefinition id, else the element id; Clone carries it.
func TestItemAwareElementName(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("explicit name wins over the item-definition id", func(t *testing.T) {
		iae := data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(1),
				foundation.WithID("idef-id")),
			data.ReadyDataState)

		require.Equal(t, "idef-id", iae.Name()) // fallback before SetName

		iae.SetName("scratch")
		require.Equal(t, "scratch", iae.Name())
	})

	t.Run("clone carries the explicit name", func(t *testing.T) {
		iae := data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(1),
				foundation.WithID("idef-id")),
			data.ReadyDataState)
		iae.SetName("named")

		clone, err := iae.Clone()
		require.NoError(t, err)
		require.Equal(t, "named", clone.Name())
	})
}

// TestAssociationNames covers the DataObject-binding accessors (SRD-063 FR-5):
// TargetName / SourceNames expose the bound item-aware elements' scope names,
// by which the runtime resolves the per-instance DataObject.
func TestAssociationNames(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	target := data.MustItemAwareElement(
		data.MustItemDefinition(values.NewVariable(0),
			foundation.WithID("tgt-id")),
		data.ReadyDataState)
	target.SetName("target-do")

	// a DataObject input association binds a single source (§10.4.1); the
	// validator rejects multi-source without a transformation.
	src := data.MustItemAwareElement(
		data.MustItemDefinition(values.NewVariable(1),
			foundation.WithID("s1")),
		data.ReadyDataState)
	src.SetName("source-a")

	a, err := data.NewAssociation(target, data.WithSource(src))
	require.NoError(t, err)

	require.Equal(t, "target-do", a.TargetName())
	require.Equal(t, []string{"source-a"}, a.SourceNames())
}

// TestAssociationDataStoreRef covers the engine-store binding on an association
// (SRD-064 FR-4): WithDataStoreRef sets it, DataStoreRef() reports it, and an
// ordinary association reports empty.
func TestAssociationDataStoreRef(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	src := func(id string) *data.ItemAwareElement {
		return data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(0),
				foundation.WithID(id)),
			data.ReadyDataState)
	}

	bound, err := data.NewAssociation(src("t"),
		data.WithSource(src("s")), data.WithDataStoreRef("orders"))
	require.NoError(t, err)
	require.Equal(t, "orders", bound.DataStoreRef())

	plain, err := data.NewAssociation(src("t2"), data.WithSource(src("s2")))
	require.NoError(t, err)
	require.Empty(t, plain.DataStoreRef())
}
