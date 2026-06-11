package instance

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTrackStateString(t *testing.T) {
	cases := map[trackState]string{
		TrackCreated:            "TrackCreated",
		TrackReady:              "TrackReady",
		TrackExecutingStep:      "TrackExecutingStep",
		TrackProcessStepResults: "TrackProcessStepResults",
		TrackWaitForEvent:       "TrackWaitForEvent",
		TrackAwaitingMerge:      "TrackAwaitingMerge",
		TrackMerged:             "TrackMerged",
		TrackEnded:              "TrackEnded",
		TrackCanceled:           "TrackCanceled",
		TrackFailed:             "TrackFailed",
	}

	for st, want := range cases {
		require.Equal(t, want, st.String())
	}
}

func TestTokenStateForAwaitingMerge(t *testing.T) {
	// a token waiting at a synchronizing join is still present (Alive), not yet
	// consumed.
	require.Equal(t, TokenAlive, tokenStateFor(TrackAwaitingMerge))
	// once merged it is consumed.
	require.Equal(t, TokenConsumed, tokenStateFor(TrackMerged))
}
