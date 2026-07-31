package dataobjects_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
)

// TestDataObjectClone: Clone preserves identity (name/ID) but deep-copies the
// value, so mutating the clone never touches the original (SRD-063 FR-2
// per-instance isolation).
func TestDataObjectClone(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	idef, err := data.NewItemDefinition(values.NewVariable(100))
	require.NoError(t, err)
	do, err := dataobjects.New("order", idef, data.ReadyDataState)
	require.NoError(t, err)

	clone, err := do.Clone()
	require.NoError(t, err)

	require.Equal(t, do.Name(), clone.Name(), "identity preserved")
	require.Equal(t, do.ID(), clone.ID())

	require.NoError(t,
		clone.ItemDefinition().Structure().Update(t.Context(), 999))
	require.EqualValues(t, 999,
		clone.ItemDefinition().Structure().Get(t.Context()))
	require.EqualValues(t, 100,
		do.ItemDefinition().Structure().Get(t.Context()),
		"the original DataObject value is unchanged (isolation)")
}

// TestCloneDataObjects covers the slice cloner: nil→nil, deep-copy of each, and
// the error path when an element can't be cloned (SRD-063 FR-2).
func TestCloneDataObjects(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	cn, err := dataobjects.CloneDataObjects(nil)
	require.NoError(t, err)
	require.Nil(t, cn)

	idefA, err := data.NewItemDefinition(values.NewVariable(1))
	require.NoError(t, err)
	doA, err := dataobjects.New("a", idefA, data.ReadyDataState)
	require.NoError(t, err)

	cloned, err := dataobjects.CloneDataObjects([]*dataobjects.DataObject{doA})
	require.NoError(t, err)
	require.Len(t, cloned, 1)
	require.NoError(t,
		cloned[0].ItemDefinition().Structure().Update(t.Context(), 5))
	require.EqualValues(t, 1,
		doA.ItemDefinition().Structure().Get(t.Context()),
		"the original is isolated from the clone")

	// error path: a DataObject with a nil value cannot be cloned.
	badIdef, err := data.NewItemDefinition(nil)
	require.NoError(t, err)
	bad, err := dataobjects.New("bad", badIdef, data.ReadyDataState)
	require.NoError(t, err)

	_, err = bad.Clone()
	require.Error(t, err, "cloning a nil-value DataObject fails")

	_, err = dataobjects.CloneDataObjects([]*dataobjects.DataObject{bad})
	require.Error(t, err, "the slice cloner propagates a per-element failure")
}
