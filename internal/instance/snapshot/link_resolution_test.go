package snapshot_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// linkProc builds start → throw"go"  and  catch"go" → end — a paired Link, the
// throw a redirect origin (incoming only), the catch a flow entry (outgoing
// only). It returns the process plus the throw and end node IDs.
func linkProc(t *testing.T) (p *process.Process, throwID, endID string) {
	t.Helper()

	p, err := process.New("link-proc")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	thr, err := events.NewIntermediateThrowEvent(
		"thr", events.MustLinkEventDefinition("go"))
	require.NoError(t, err)

	cat, err := events.NewIntermediateCatchEvent(
		"cat", events.MustLinkEventDefinition("go"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, thr, cat, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, thr)
	require.NoError(t, err)
	_, err = flow.Link(cat, end)
	require.NoError(t, err)

	return p, thr.ID(), end.ID()
}

// throwOf fetches the Link throw clone from a snapshot's node set.
func throwOf(t *testing.T, s *snapshot.Snapshot, id string) *events.IntermediateThrowEvent {
	t.Helper()

	n, ok := s.Nodes[id]
	require.True(t, ok)

	ite, ok := n.(*events.IntermediateThrowEvent)
	require.True(t, ok)

	return ite
}

// TestSnapshotResolvesLinkEdge proves snapshot.New wires the Link throw to its
// target catch (via WireClonedGraph → resolveLinkEdges), so the throw's Exec
// redirects to the catch's outgoing flow — with zero runner involvement
// (SRD-057 M2, FR-4/FR-5).
func TestSnapshotResolvesLinkEdge(t *testing.T) {
	ctx := context.Background()

	p, throwID, endID := linkProc(t)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	got, err := throwOf(t, s, throwID).Exec(ctx, nil)
	require.NoError(t, err)

	// the throw yields the target catch's downstream: the single flow catch→end.
	require.Len(t, got, 1)
	require.Equal(t, endID, got[0].Target().ID())
}

// TestSnapshotCloneIsolatesLinkEdge proves each per-instance clone resolves its
// OWN throw→catch edge — a throw in one instance never redirects into another's
// graph (SRD-057 M2, FR-4; ADR-009 per-instance isolation).
func TestSnapshotCloneIsolatesLinkEdge(t *testing.T) {
	ctx := context.Background()

	p, throwID, endID := linkProc(t)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	c1, err := s.Clone()
	require.NoError(t, err)
	c2, err := s.Clone()
	require.NoError(t, err)

	got1, err := throwOf(t, c1, throwID).Exec(ctx, nil)
	require.NoError(t, err)
	got2, err := throwOf(t, c2, throwID).Exec(ctx, nil)
	require.NoError(t, err)

	require.Len(t, got1, 1)
	require.Len(t, got2, 1)

	// both redirect to their own instance's end...
	require.Equal(t, endID, got1[0].Target().ID())
	require.Equal(t, endID, got2[0].Target().ID())

	// ...but the resolved edges are distinct flow objects per instance.
	require.NotSame(t, got1[0], got2[0])
}
