package scope

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/datastore/memstore"
	"github.com/stretchr/testify/require"
)

// TestFrameDataStores covers the Data Store registry wiring on a frame
// (SRD-068 FR-4): nil until set, then the registry SetDataStores wired.
func TestFrameDataStores(t *testing.T) {
	pl, err := New(RootDataPath, nil)
	require.NoError(t, err)

	f, err := NewFrame("track", "node", pl.Root(), pl)
	require.NoError(t, err)

	require.Nil(t, f.DataStores())

	reg := memstore.NewRegistry()
	f.SetDataStores(reg)
	require.Equal(t, reg, f.DataStores())
}
