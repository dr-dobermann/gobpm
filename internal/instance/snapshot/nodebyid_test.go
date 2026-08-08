package snapshot_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// TestNodeByID: the deep resolver finds top-level AND nested nodes —
// a mid-composite checkpoint records inner nodes (SRD-082 FR-5).
func TestNodeByID(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("nbi", foundation.WithID("nbi"))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", foundation.WithID("nbi-start"))
	require.NoError(t, err)

	body, err := activities.NewSubProcess("body", foundation.WithID("nbi-body"))
	require.NoError(t, err)

	bStart, err := events.NewStartEvent("b-start",
		foundation.WithID("nbi-b-start"))
	require.NoError(t, err)
	bEnd, err := events.NewEndEvent("b-end", foundation.WithID("nbi-b-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{bStart, bEnd} {
		require.NoError(t, body.Add(e))
	}

	_, err = flow.Link(bStart, bEnd)
	require.NoError(t, err)

	end, err := events.NewEndEvent("end", foundation.WithID("nbi-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, body, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, body)
	require.NoError(t, err)
	_, err = flow.Link(body, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	top, ok := s.NodeByID("nbi-body")
	require.True(t, ok, "a top-level node resolves from the index")
	require.Equal(t, "nbi-body", top.ID())

	inner, ok := s.NodeByID("nbi-b-start")
	require.True(t, ok, "an INNER node resolves through the deep walk")
	require.Equal(t, "nbi-b-start", inner.ID())

	_, ok = s.NodeByID("nowhere")
	require.False(t, ok)
}
