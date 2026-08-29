package activities_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// fakeDOElement lies about its EType (DataObjectElement) while not being a
// *dataobjects.DataObject — it exercises the comma-ok guard in SubProcess.Add.
type fakeDOElement struct{ id string }

func (fakeDOElement) Docs() []*foundation.Documentation { return nil }
func (f fakeDOElement) ID() string                      { return f.id }
func (fakeDOElement) Name() string                      { return "fake" }
func (fakeDOElement) Container() flow.Container         { return nil }
func (fakeDOElement) EType() flow.ElementType           { return flow.DataObjectElement }
func (fakeDOElement) BindTo(flow.Container) error       { return nil }
func (fakeDOElement) Unbind() error                     { return nil }

// spDataObject builds a ready DataObject named name holding v (SRD-063 FR-4).
func spDataObject(
	t *testing.T, name string, v any,
) *dataobjects.DataObject {
	t.Helper()

	do, err := dataobjects.New(name,
		data.MustItemDefinition(values.NewVariable(v),
			foundation.WithID(name+"-id")),
		data.ReadyDataState)
	require.NoError(t, err)

	return do
}

// TestSubProcessDataObjects covers the SubProcess-level Data Object plumbing
// (SRD-063 FR-4): registration off the node graph, the one-name-space
// duplicate guard, and the per-instance deep-clone isolation.
func TestSubProcessDataObjects(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("registers and lists a sub-process data object", func(t *testing.T) {
		sp := noneStartSP(t, "with-do")
		require.NoError(t, sp.Add(spDataObject(t, "counter", 0)))

		dd := sp.DataObjects()
		require.Len(t, dd, 1)
		require.Equal(t, "counter", dd[0].Name())
	})

	t.Run("rejects a duplicate data object name", func(t *testing.T) {
		sp := noneStartSP(t, "dup-do")
		require.NoError(t, sp.Add(spDataObject(t, "dupe", 1)))
		require.Error(t, sp.Add(spDataObject(t, "dupe", 2)))
	})

	t.Run("clone deep-copies data objects (isolation)", func(t *testing.T) {
		ctx := context.Background()
		sp := noneStartSP(t, "iso")
		require.NoError(t, sp.Add(spDataObject(t, "shared", 10)))
		require.NoError(t, sp.Validate())

		cn, err := sp.Clone()
		require.NoError(t, err)
		csp, ok := cn.(*activities.SubProcess)
		require.True(t, ok)

		cloneDD := csp.DataObjects()
		require.Len(t, cloneDD, 1)

		// mutating the clone's data object must not touch the source.
		require.NoError(t, cloneDD[0].Subject().Structure().Update(ctx, 99))

		srcDD := sp.DataObjects()
		require.Len(t, srcDD, 1)
		require.Equal(t, 10, srcDD[0].Subject().Structure().Get(ctx))
		require.Equal(t, 99, cloneDD[0].Subject().Structure().Get(ctx))
	})

	t.Run("rejects a DataObjectElement that is not a *DataObject",
		func(t *testing.T) {
			sp := noneStartSP(t, "liar")
			require.Error(t, sp.Add(fakeDOElement{id: "liar-do"}))
		})

	t.Run("clone fails when a data object cannot be cloned",
		func(t *testing.T) {
			sp := noneStartSP(t, "bad-clone")

			// a nil-value DataObject registers but fails to Clone.
			badIdef, err := data.NewItemDefinition(nil)
			require.NoError(t, err)
			bad, err := dataobjects.New("bad", badIdef, data.ReadyDataState)
			require.NoError(t, err)
			require.NoError(t, sp.Add(bad))

			_, err = sp.Clone()
			require.Error(t, err)
		})
}
