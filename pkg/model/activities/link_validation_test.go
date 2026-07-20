package activities_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

// TestSubProcessValidatesLinkPairing proves SubProcess.Validate runs the Link
// pairing check over its OWN container only — so a Link never pairs across the
// parent/sub-process boundary (SRD-057 T-3, FR-3; ADR-006 v.4 §2.8 single-level).
func TestSubProcessValidatesLinkPairing(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("an unpaired Link throw inside a sub-process fails its Validate",
		func(t *testing.T) {
			sp, err := activities.NewSubProcess("sub")
			require.NoError(t, err)

			start, err := events.NewStartEvent("s")
			require.NoError(t, err)

			thr, err := events.NewIntermediateThrowEvent(
				"thr", events.MustLinkEventDefinition("inner"))
			require.NoError(t, err)

			require.NoError(t, sp.Add(start))
			require.NoError(t, sp.Add(thr))

			_, err = flow.Link(start, thr)
			require.NoError(t, err)

			err = sp.Validate()
			require.ErrorContains(t, err, "Link")
			require.ErrorContains(t, err, "inner")
		})
}
