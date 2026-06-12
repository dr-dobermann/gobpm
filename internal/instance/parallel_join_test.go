package instance

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// link is a small helper: flow.Link with the SequenceSource/Target assertions.
func link(t *testing.T, src, trg flow.Element) {
	t.Helper()

	s, ok := src.(flow.SequenceSource)
	require.True(t, ok)

	g, ok := trg.(flow.SequenceTarget)
	require.True(t, ok)

	_, err := flow.Link(s, g)
	require.NoError(t, err)
}

// countMerged returns how many of the instance's tracks ended Merged.
func countMerged(inst *Instance) int {
	n := 0

	for _, tr := range inst.tracks {
		if tr.inState(TrackMerged) {
			n++
		}
	}

	return n
}

// TestParallelJoinSynchronizes verifies the Parallel synchronizing join
// (SRD-005 M3): a split forks two branches that converge on a join; the join
// fires once when both arrive — one track is Merged (its token Consumed), the
// surviving track continues to the End.
//
//	start ─> split ─┬─> join ─> end
//	                └─> ┘   (two flows split->join)
func TestParallelJoinSynchronizes(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("parallel-join")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	split, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	join, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, join, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, split)
	link(t, split, join) // branch 1
	link(t, split, join) // branch 2
	link(t, join, end)

	require.Len(t, join.Incoming(), 2)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	leak := assertNoGoroutineLeak(t)
	defer leak()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, inst.Run(ctx))

	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond,
		"the join should fire and the surviving branch reach End")

	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 2, inst.trackCount.Load(),
		"split into two branches -> 2 tracks")
	require.Equal(t, 1, countMerged(inst),
		"exactly one branch is merged away at the join")
	require.Empty(t, inst.GetTokens(), "no active tokens after completion")
}

// TestParallelJoinLineageAcyclic guards the synchronizing join against the
// lineage bug where folding the absorbed track ids into the survivor's prev made
// TokenHistory().ParentID point at a track the survivor itself had spawned — a
// cycle (T0->T1->T0) that makes the lineage tree unbuildable. Convergence must
// be carried by the absorbed track's own Consumed entry, leaving every ParentID
// a genuine creation edge, so walking ParentID from any track terminates at the
// root without revisiting a track (ADR-005 §2.4).
//
//	start ─> split ─┬─> join ─> end
//	                └─> ┘
func TestParallelJoinLineageAcyclic(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("parallel-join-lineage")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	split, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	join, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, join, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, split)
	link(t, split, join)
	link(t, split, join)
	link(t, join, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		mockeventproc.NewMockEventProducer(t), nil)
	require.NoError(t, err)

	leak := assertNoGoroutineLeak(t)
	defer leak()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, inst.Run(ctx))
	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond)

	require.Equal(t, 1, countMerged(inst), "exactly one branch merged")

	hist := inst.TokenHistory()
	require.NotEmpty(t, hist)

	parent := make(map[string]string, len(hist))
	for _, tp := range hist {
		parent[tp.TrackID] = tp.ParentID
	}

	// walking ParentID up from every track must reach the root ("") without
	// revisiting a track (no cycle) and without a dangling reference.
	for _, tp := range hist {
		seen := map[string]bool{}

		for id := tp.TrackID; id != ""; {
			require.False(t, seen[id], "lineage cycle through %q", id)
			seen[id] = true

			next, ok := parent[id]
			require.True(t, ok, "track %q references an unknown parent", id)
			id = next
		}
	}
}

// TestParallelJoinMixed verifies a mixed gateway (N incoming AND M outgoing):
// the join half synchronizes, then the surviving track executes and the split
// half forks on every outgoing flow (ADR-005 §2.7 join -> execute -> fork).
//
//	start ─> split ─┬─> mixed ─┬─> end1
//	                └─> ┘       └─> end2
func TestParallelJoinMixed(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("parallel-mixed")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	split, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	mixed, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Mixed))
	require.NoError(t, err)

	end1, err := events.NewEndEvent("end1")
	require.NoError(t, err)

	end2, err := events.NewEndEvent("end2")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, mixed, end1, end2} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, split)
	link(t, split, mixed) // branch 1 into the join half
	link(t, split, mixed) // branch 2 into the join half
	link(t, mixed, end1)  // fork half
	link(t, mixed, end2)

	require.Len(t, mixed.Incoming(), 2)
	require.Len(t, mixed.Outgoing(), 2)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	leak := assertNoGoroutineLeak(t)
	defer leak()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, inst.Run(ctx))

	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond,
		"mixed gateway joins then forks to both Ends")

	require.NoError(t, inst.LastErr())
	require.Equal(t, 1, countMerged(inst),
		"one branch merged at the join half")
	// 1 initial + 1 split fork + 1 mixed fork (join half synchronizes 2->1,
	// fork half splits 1->2).
	require.EqualValues(t, 3, inst.trackCount.Load())
	require.Empty(t, inst.GetTokens())
}
