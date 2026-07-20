package process_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

func linkThrowNode(t *testing.T, id, name string) *events.IntermediateThrowEvent {
	t.Helper()

	e, err := events.NewIntermediateThrowEvent(
		id, events.MustLinkEventDefinition(name))
	require.NoError(t, err)

	return e
}

func linkCatchNode(t *testing.T, id, name string) *events.IntermediateCatchEvent {
	t.Helper()

	e, err := events.NewIntermediateCatchEvent(
		id, events.MustLinkEventDefinition(name))
	require.NoError(t, err)

	return e
}

// TestProcessValidatesLinkPairing proves Process.Validate runs the per-container
// Link pairing check at registration (SRD-057 T-3, FR-3).
func TestProcessValidatesLinkPairing(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("a paired Link passes registration validation", func(t *testing.T) {
		p, err := process.New("linked")
		require.NoError(t, err)

		start, err := events.NewStartEvent("start")
		require.NoError(t, err)
		end, err := events.NewEndEvent("end")
		require.NoError(t, err)

		thr := linkThrowNode(t, "thr", "go")
		cat := linkCatchNode(t, "cat", "go")

		for _, e := range []flow.Element{start, thr, cat, end} {
			require.NoError(t, p.Add(e))
		}

		_, err = flow.Link(start, thr) // throw source: incoming only
		require.NoError(t, err)
		_, err = flow.Link(cat, end) // catch target: outgoing only
		require.NoError(t, err)

		require.NoError(t, p.Validate())
	})

	t.Run("an unpaired Link throw fails registration validation", func(t *testing.T) {
		p, err := process.New("orphan")
		require.NoError(t, err)

		start, err := events.NewStartEvent("start")
		require.NoError(t, err)

		thr := linkThrowNode(t, "thr", "nowhere")

		require.NoError(t, p.Add(start))
		require.NoError(t, p.Add(thr))

		_, err = flow.Link(start, thr)
		require.NoError(t, err)

		err = p.Validate()
		require.ErrorContains(t, err, "Link")
		require.ErrorContains(t, err, "nowhere")
	})
}
