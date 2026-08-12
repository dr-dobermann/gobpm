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

// TestWalk: the exported deep traversal reaches nested nodes and honors an
// early stop. Its caller is registration-time model validation (SRD-088 FR-8),
// which must inspect EVERY node rather than find one — a Script Task inside a
// Sub-Process demands its script format just as much as a top-level one.
func TestWalk(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("wlk", foundation.WithID("wlk"))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", foundation.WithID("wlk-start"))
	require.NoError(t, err)

	body, err := activities.NewSubProcess("body", foundation.WithID("wlk-body"))
	require.NoError(t, err)

	bStart, err := events.NewStartEvent("b-start",
		foundation.WithID("wlk-b-start"))
	require.NoError(t, err)
	bEnd, err := events.NewEndEvent("b-end", foundation.WithID("wlk-b-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{bStart, bEnd} {
		require.NoError(t, body.Add(e))
	}

	_, err = flow.Link(bStart, bEnd)
	require.NoError(t, err)

	end, err := events.NewEndEvent("end", foundation.WithID("wlk-end"))
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

	seen := map[string]bool{}

	require.True(t, s.Walk(func(n flow.Node) bool {
		seen[n.ID()] = true

		return true
	}), "a walk nobody stops reports completion")

	for _, id := range []string{
		"wlk-start", "wlk-body", "wlk-end", "wlk-b-start", "wlk-b-end",
	} {
		require.True(t, seen[id], "the walk must visit %q", id)
	}

	// Stopping early must propagate out, so a validator that has already found
	// what it needs can quit instead of walking the rest of the graph.
	var visited int

	require.False(t, s.Walk(func(flow.Node) bool {
		visited++

		return false
	}), "a stopped walk reports it")
	require.Equal(t, 1, visited)
}
