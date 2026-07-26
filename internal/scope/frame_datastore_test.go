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

// TestFrameDataMovements covers the Data Object / Data Store movement accumulator
// on a frame (SRD-063 / SRD-068): empty until recorded, then the movements the
// reroute noted in occurrence order — a Data Object carries no store ref, a Data
// Store carries its dataStoreRef.
func TestFrameDataMovements(t *testing.T) {
	pl, err := New(RootDataPath, nil)
	require.NoError(t, err)

	f, err := NewFrame("track", "node", pl.Root(), pl)
	require.NoError(t, err)

	require.Empty(t, f.DataMovements())

	f.RecordDataMovement(false, false, "order", "")  // Data Object read
	f.RecordDataMovement(false, true, "result", "")  // Data Object write
	f.RecordDataMovement(true, false, "cust", "kv")  // Data Store read
	f.RecordDataMovement(true, true, "total", "kv")  // Data Store write

	require.Equal(t, []DataMovement{
		{Name: "order", StoreRef: "", EngineStore: false, Write: false},
		{Name: "result", StoreRef: "", EngineStore: false, Write: true},
		{Name: "cust", StoreRef: "kv", EngineStore: true, Write: false},
		{Name: "total", StoreRef: "kv", EngineStore: true, Write: true},
	}, f.DataMovements())
}
