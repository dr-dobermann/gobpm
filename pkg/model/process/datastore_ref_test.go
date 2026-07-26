package process_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	datastores "github.com/dr-dobermann/gobpm/pkg/model/data_stores"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// TestProcessRegistersDataStoreReference: a DataStoreReference is registered on
// the Process via Add (containment only — not seeded into scope), exposed by
// DataStoreReferences(), with a unique name (SRD-068 FR-3).
func TestProcessRegistersDataStoreReference(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ref := func(name string) *datastores.DataStoreReference {
		r, err := datastores.New(name, "orders",
			data.MustItemDefinition(
				values.NewVariable(0), foundation.WithID(name+"-id")),
			data.ReadyDataState)
		require.NoError(t, err)

		return r
	}

	p, err := process.New("p")
	require.NoError(t, err)

	require.NoError(t, p.Add(ref("a")))
	require.Len(t, p.DataStoreReferences(), 1)

	// a duplicate name is rejected.
	require.Error(t, p.Add(ref("a")))

	// an element lying about its type is rejected.
	require.Error(t,
		p.Add(fakeElement{id: "liar", etype: flow.DataStoreReferenceElement}))
}
