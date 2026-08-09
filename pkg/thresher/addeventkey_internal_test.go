package thresher

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
)

// hubWithoutKeys is an EventHub that does NOT offer the optional
// AddEventKey capability — the structural probe must no-op on it.
type hubWithoutKeys struct{ eventproc.EventHub }

// TestAddEventKeyPassthrough pins the engine-level forward (SRD-085
// FR-3): an empty definition id refuses, a hub without the optional
// capability no-ops, and a capable hub receives the call.
func TestAddEventKeyPassthrough(t *testing.T) {
	th, err := New("addkey")
	require.NoError(t, err)

	require.ErrorContains(t, th.AddEventKey("  ", "v"),
		"empty event definition id")

	th.eventHub = hubWithoutKeys{}
	require.NoError(t, th.AddEventKey("d1", "v"),
		"a hub without the capability is a benign no-op")

	// the real hub answers for an unknown definition without error
	// (no waiter to extend).
	th2, err := New("addkey-real")
	require.NoError(t, err)
	require.NoError(t, th2.AddEventKey("no-such-def", "v"))
}
