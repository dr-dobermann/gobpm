package activities_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	datastores "github.com/dr-dobermann/gobpm/pkg/model/data_stores"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// fakeDSElement lies about its EType (DataStoreReferenceElement) while not being
// a *DataStoreReference — exercises the comma-ok guard in SubProcess.Add.
type fakeDSElement struct{ id string }

func (fakeDSElement) Docs() []*foundation.Documentation { return nil }
func (f fakeDSElement) ID() string                      { return f.id }
func (fakeDSElement) Name() string                      { return "fake" }
func (fakeDSElement) Container() flow.Container         { return nil }
func (fakeDSElement) EType() flow.ElementType {
	return flow.DataStoreReferenceElement
}
func (fakeDSElement) BindTo(flow.Container) error { return nil }
func (fakeDSElement) Unbind() error               { return nil }

// TestSubProcessDataStoreReferences covers the SubProcess-level Data Store
// Reference containment (SRD-064 FR-3): register/list, the duplicate-name and
// type-mismatch guards, and that Clone carries the (shared) references.
func TestSubProcessDataStoreReferences(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ref := func(name string) *datastores.DataStoreReference {
		r, err := datastores.New(name, "orders",
			data.MustItemDefinition(
				values.NewVariable(0), foundation.WithID(name+"-id")),
			data.ReadyDataState)
		require.NoError(t, err)

		return r
	}

	t.Run("register, list, duplicate reject", func(t *testing.T) {
		sp := noneStartSP(t, "with-ref")
		require.NoError(t, sp.Add(ref("a")))
		require.Len(t, sp.DataStoreReferences(), 1)
		require.Error(t, sp.Add(ref("a")))
	})

	t.Run("type-mismatch guard", func(t *testing.T) {
		sp := noneStartSP(t, "liar-ref")
		require.Error(t, sp.Add(fakeDSElement{id: "liar"}))
	})

	t.Run("clone carries the references", func(t *testing.T) {
		sp := noneStartSP(t, "clone-ref")
		require.NoError(t, sp.Add(ref("shared")))
		require.NoError(t, sp.Validate())

		cn, err := sp.Clone()
		require.NoError(t, err)
		csp, ok := cn.(*activities.SubProcess)
		require.True(t, ok)
		require.Len(t, csp.DataStoreReferences(), 1)
	})
}
