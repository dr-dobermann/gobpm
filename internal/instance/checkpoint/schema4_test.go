package checkpoint

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// SRD-082 T-1 — the schema-4 position records round-trip, the schema
// stamps 4, and the future-schema refusal still fires.
func TestSchemaFourRoundTrip(t *testing.T) {
	doc := &Document{
		InstanceID: "i1",
		ProcessID:  "p1",
		Status:     "Active",
		Calls: []CallRecord{{
			ChildID: "child-1", NodeID: "call-1", TrackID: "t-1",
		}},
		MIGroups: []MIGroupRecord{{
			HostTrack: "t-2",
			Staging:   json.RawMessage(`{"kind":"array","items":[]}`),
			Open: []OpenScope{
				{Path: "/p/mi-0", Ordinal: 0},
				{Path: "/p/mi-2", Ordinal: 2},
			},
			N:       3,
			Pending: 1,
		}},
		Sweeps: []SweepRecord{{
			ThrowerTrack: "t-3",
			TxHostTrack:  "t-4",
			ScopePath:    "/p/tx",
			Queue: []LedgerRecord{{
				ScopePath: "/p/tx", ActivityID: "a-1", Ordinal: 1,
			}},
			Running: &LedgerRecord{
				ScopePath: "/p/tx", ActivityID: "a-0", Ordinal: 0,
			},
			Wait: true,
		}},
		Tracks: []TrackRecord{{
			ID: "t-2", State: "TrackExecutingStep", NodeID: "mi-host",
			ScopePath: "/p",
			MI: &MIRecord{
				Staging:      json.RawMessage(`{"kind":"array","items":[]}`),
				N:            3,
				Completed:    1,
				ConditionMet: true,
			},
		}},
	}

	raw, err := doc.Marshal()
	require.NoError(t, err)

	back, err := Unmarshal(raw)
	require.NoError(t, err)
	require.Equal(t, 4, back.Schema, "Marshal stamps the current schema")
	require.Equal(t, doc.Calls, back.Calls)
	require.Equal(t, doc.MIGroups, back.MIGroups)
	require.Equal(t, doc.Sweeps, back.Sweeps)
	require.Equal(t, doc.Tracks[0].MI, back.Tracks[0].MI)
}

func TestFutureSchemaStillRefused(t *testing.T) {
	raw := []byte(`{"instance_id":"i","process_id":"p","schema":5}`)

	_, err := Unmarshal(raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "schema 1..4")
}

// TestEncodeDecodeValue pins the staging codec (SRD-082 FR-1): a
// canonical array round-trips; garbage refuses loud.
func TestEncodeDecodeValue(t *testing.T) {
	ctx := t.Context()

	raw, err := EncodeValue(ctx, "test",
		values.NewArray[any]("a", 2, true))
	require.NoError(t, err)

	back, err := DecodeValue(ctx, raw)
	require.NoError(t, err)

	col, ok := back.(data.Collection)
	require.True(t, ok)

	got := make([]any, 0, col.Count())

	for _, el := range col.GetAll(ctx) {
		v, isVal := el.(data.Value)
		require.True(t, isVal, "decoded elements are canonical values")

		got = append(got, v.Get(ctx))
	}

	require.Equal(t, []any{"a", 2, true}, got)

	_, err = DecodeValue(ctx, []byte("{not json"))
	require.Error(t, err)
}

// TestEncodeValueRefusesUncodable: an unserializable element is a loud
// checkpoint error, never a silent skip (ADR-033 §2.1.3).
func TestEncodeValueRefusesUncodable(t *testing.T) {
	_, err := EncodeValue(t.Context(), "test",
		values.NewArray[any](make(chan int)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "isn't checkpoint-codable")
}

// TestEncodeDecodeValueWithHoles: a pre-sized parallel staging carries
// nil slots — the codec's explicit nil kind round-trips them (before
// it, one nil poisoned every later checkpoint of the instance).
func TestEncodeDecodeValueWithHoles(t *testing.T) {
	ctx := t.Context()

	raw, err := EncodeValue(ctx, "test",
		values.NewArray[any]("filled", nil, nil))
	require.NoError(t, err)

	back, err := DecodeValue(ctx, raw)
	require.NoError(t, err)

	col, ok := back.(data.Collection)
	require.True(t, ok)

	all := col.GetAll(ctx)
	require.Len(t, all, 3)
	require.Nil(t, all[1])
	require.Nil(t, all[2])

	v, isVal := all[0].(data.Value)
	require.True(t, isVal)
	require.Equal(t, "filled", v.Get(ctx))
}
