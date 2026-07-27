package checkpoint_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
)

// TestDocumentRoundTrip: a full document survives Marshal/Unmarshal
// with its schema stamped.
func TestDocumentRoundTrip(t *testing.T) {
	deadline := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	d := &checkpoint.Document{
		InstanceID: "inst-1",
		ProcessID:  "order",
		Version:    3,
		Status:     "Active",
		ConvKeys:   map[string]string{"orderID": "42"},
		Scopes: []checkpoint.ScopeRecord{
			{Path: "/order", Data: []byte(`[]`)},
		},
		Ledgers: []checkpoint.LedgerRecord{
			{ScopePath: "/order", ActivityID: "book", Ordinal: 0},
		},
		Tracks: []checkpoint.TrackRecord{
			{
				ID:     "tr-1",
				State:  "TrackWaitForEvent",
				NodeID: "wait-timer",
				Timer: &checkpoint.TimerDescriptor{
					Deadline:   deadline,
					CyclesLeft: 2,
				},
			},
		},
	}

	raw, err := d.Marshal()
	require.NoError(t, err)

	back, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)

	require.Equal(t, checkpoint.CurrentSchema, back.Schema)
	require.Equal(t, "inst-1", back.InstanceID)
	require.Equal(t, 3, back.Version)
	require.Len(t, back.Tracks, 1)
	require.NotNil(t, back.Tracks[0].Timer)
	require.True(t, deadline.Equal(back.Tracks[0].Timer.Deadline))
	require.Equal(t, 2, back.Tracks[0].Timer.CyclesLeft)
	require.Equal(t, "42", back.ConvKeys["orderID"])
}

// TestDocumentValidation: the loud gates.
func TestDocumentValidation(t *testing.T) {
	t.Run("nil document",
		func(t *testing.T) {
			var d *checkpoint.Document

			_, err := d.Marshal()
			require.Error(t, err)
		})

	t.Run("missing identity",
		func(t *testing.T) {
			_, err := (&checkpoint.Document{InstanceID: "x"}).Marshal()
			require.Error(t, err)
			require.Contains(t, err.Error(), "instance and process ids")
		})

	t.Run("empty payload",
		func(t *testing.T) {
			_, err := checkpoint.Unmarshal(nil)
			require.Error(t, err)
		})

	t.Run("garbage payload",
		func(t *testing.T) {
			_, err := checkpoint.Unmarshal([]byte("{"))
			require.Error(t, err)
			require.Contains(t, err.Error(), "doesn't parse")
		})

	t.Run("unknown schema is the migration gate",
		func(t *testing.T) {
			_, err := checkpoint.Unmarshal([]byte(
				`{"schema":99,"instance_id":"i","process_id":"p",` +
					`"status":"Active","scopes":[],"tracks":[]}`))
			require.Error(t, err)
			require.Contains(t, err.Error(), "unsupported checkpoint schema")
		})

	t.Run("nameless checkpoint refused",
		func(t *testing.T) {
			_, err := checkpoint.Unmarshal([]byte(
				`{"schema":1,"instance_id":"","process_id":"",` +
					`"status":"Active","scopes":[],"tracks":[]}`))
			require.Error(t, err)
			require.Contains(t, err.Error(), "names no instance/process")
		})
}

// TestMarshalInvalidRawMessage: an invalid embedded RawMessage makes
// the document serialization itself fail — the loud relay.
func TestMarshalInvalidRawMessage(t *testing.T) {
	d := &checkpoint.Document{
		InstanceID: "i",
		ProcessID:  "p",
		Scopes: []checkpoint.ScopeRecord{
			{Path: "/p", Data: []byte("{oops")},
		},
	}

	_, err := d.Marshal()
	require.Error(t, err)
	require.Contains(t, err.Error(), "serialization failed")
}
