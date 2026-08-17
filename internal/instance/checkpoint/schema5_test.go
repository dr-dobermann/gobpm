package checkpoint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// SRD-083 T-1 — the schema-5 Ad-Hoc records round-trip, the schema
// stamps 5, and the future-schema refusal names the widened range.
func TestSchemaFiveRoundTrip(t *testing.T) {
	doc := &Document{
		InstanceID: "i1",
		ProcessID:  "p1",
		Status:     "Active",
		AdHoc: []AdHocRecord{{
			HostTrack:  "t-1",
			ScopePath:  "/p/triage",
			Completed:  map[string]int{"a": 1, "b": 2},
			Offered:    []string{"b", "c"},
			StopReason: "completion-condition",
			Stopped:    true,
		}},
		Tracks: []TrackRecord{{
			ID: "t-2", State: "TrackExecutingStep", NodeID: "b",
			ScopePath:     "/p/triage",
			AdHocActivity: "b",
		}},
	}

	raw, err := doc.Marshal()
	require.NoError(t, err)

	back, err := Unmarshal(raw)
	require.NoError(t, err)
	require.Equal(t, CurrentSchema, back.Schema, "Marshal stamps the current schema")
	require.Equal(t, doc.AdHoc, back.AdHoc)
	require.Equal(t, "b", back.Tracks[0].AdHocActivity)
}

// SRD-090.A T-5 — an iterated leaf's executor set round-trips, and a
// document written before the move (no Iteration, an MI mirror instead)
// still reads: the compatibility direction that matters, since every
// document captured by the previous release is of that shape.
func TestSchemaSixIterationRoundTrip(t *testing.T) {
	doc := &Document{
		InstanceID: "i1",
		ProcessID:  "p1",
		Status:     "Active",
		Tracks: []TrackRecord{{
			ID: "t-1", State: "TrackExecutingStep", NodeID: "charge",
			ScopePath: "/p",
			Iteration: &IterationRecord{
				Kind:      "mi_parallel",
				N:         3,
				Completed: 1,
				Instances: []IterationInstance{
					{Ordinal: 0, State: "running"},
					{Ordinal: 1, State: "completed"},
					{Ordinal: 2, State: "running"},
				},
			},
		}},
	}

	back, err := roundTrip(t, doc)
	require.NoError(t, err)
	require.Equal(t, CurrentSchema, back.Schema)
	require.Equal(t, doc.Tracks[0].Iteration, back.Tracks[0].Iteration)
	require.Nil(t, back.Tracks[0].MI, "the mirror is retired for a leaf")

	old := &Document{
		InstanceID: "i2", ProcessID: "p1", Status: "Active",
		Tracks: []TrackRecord{{
			ID: "t-2", State: "TrackExecutingStep", NodeID: "charge",
			ScopePath: "/p",
			MI:        &MIRecord{N: 3, Completed: 2},
		}},
	}

	backOld, err := roundTrip(t, old)
	require.NoError(t, err)
	require.Nil(t, backOld.Tracks[0].Iteration,
		"a pre-move document carries no executor set")
	require.Equal(t, 2, backOld.Tracks[0].MI.Completed,
		"its position is still readable, which is what restores it")
}

// roundTrip marshals and reads a document back.
func roundTrip(t *testing.T, doc *Document) (*Document, error) {
	t.Helper()

	raw, err := doc.Marshal()
	require.NoError(t, err)

	return Unmarshal(raw)
}
