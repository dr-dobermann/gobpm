package instance

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// TestReportDataMovements (SRD-063 / SRD-068): the track publishes one fact per
// Data Object / Data Store movement recorded on the node's frame — a per-instance
// Data Object as KindDataObject (no store ref), the engine-global Data Store as
// KindDataStore carrying the store ref, each phased Read (inbound) or Written
// (outbound).
func TestReportDataMovements(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// a minimal idle instance whose report fans out to observers (not run).
	p, err := process.New("datamovement-facts")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	rec := &obsRecorder{}
	inst.AddObserver(rec.record)

	tr := &track{instance: inst}

	pl, err := scope.New(scope.RootDataPath, nil)
	require.NoError(t, err)

	f, err := scope.NewFrame("tr", "node", pl.Root(), pl)
	require.NoError(t, err)

	f.RecordDataMovement(false, false, "order", "")  // Data Object read
	f.RecordDataMovement(false, true, "result", "")  // Data Object write
	f.RecordDataMovement(true, false, "cust", "kv")  // Data Store read
	f.RecordDataMovement(true, true, "total", "kv")  // Data Store write

	tr.reportDataMovements(start, f)

	// Data Object: both directions, observer-only, no store attribute.
	doPhases := rec.phasesOf(observability.KindDataObject)
	require.True(t, doPhases[observability.PhaseRead], "a Data Object is Read")
	require.True(t, doPhases[observability.PhaseWritten], "a Data Object is Written")

	// Data Store: both directions, carrying the store ref.
	dsPhases := rec.phasesOf(observability.KindDataStore)
	require.True(t, dsPhases[observability.PhaseRead], "a Data Store is Read")
	require.True(t, dsPhases[observability.PhaseWritten], "a Data Store is Written")

	// the details: a Data Store fact carries the store ref and the key; a Data
	// Object fact carries only the name (never a store ref).
	var sawStoreWrite, sawObjRead bool

	for _, e := range rec.events {
		switch e.Kind {
		case observability.KindDataStore:
			require.Equal(t, "kv", e.Details[observability.AttrDataStore])

			if e.Phase == observability.PhaseWritten {
				require.Equal(t, "total", e.Details[observability.AttrDataName])
				sawStoreWrite = true
			}

		case observability.KindDataObject:
			require.NotContains(t, e.Details, observability.AttrDataStore)

			if e.Phase == observability.PhaseRead {
				require.Equal(t, "order", e.Details[observability.AttrDataName])
				sawObjRead = true
			}
		}
	}

	require.True(t, sawStoreWrite, "the Data Store write fact carries its key")
	require.True(t, sawObjRead, "the Data Object read fact carries its name")

	// the node attribution rides every movement fact.
	for _, e := range rec.events {
		if e.Kind == observability.KindDataObject ||
			e.Kind == observability.KindDataStore {
			require.Equal(t, start.ID(), e.NodeID)
			require.Equal(t, start.Name(), e.NodeName)
		}
	}
}
