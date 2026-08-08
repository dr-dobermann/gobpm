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
	require.Equal(t, 5, back.Schema, "Marshal stamps the current schema")
	require.Equal(t, doc.AdHoc, back.AdHoc)
	require.Equal(t, "b", back.Tracks[0].AdHocActivity)
}
