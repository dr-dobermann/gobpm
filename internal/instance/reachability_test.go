package instance

import (
	"testing"

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

// orDiamond builds:  start → split ─┬→ a → merge ─→ end
//
//	└→ b ─┘
//
// (split diverging, merge converging) and returns the process + the nodes the
// reachability tests anchor on.
func orDiamond(t *testing.T) (*process.Process, flow.Node, flow.Node, flow.Node, flow.Node) {
	t.Helper()

	p, err := process.New("or-diamond")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	split, err := gateways.NewParallelGateway(gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)
	a, err := gateways.NewParallelGateway()
	require.NoError(t, err)
	b, err := gateways.NewParallelGateway()
	require.NoError(t, err)
	merge, err := gateways.NewParallelGateway(gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, a, b, merge, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, split)
	link(t, split, a)
	link(t, split, b)
	link(t, a, merge)
	link(t, b, merge)
	link(t, merge, end)

	return p, split, a, b, merge
}

func TestReachesOccupied(t *testing.T) {
	_, split, a, b, merge := orDiamond(t)

	t.Run("live token upstream is reachable",
		func(t *testing.T) {
			require.True(t,
				reachesOccupied(a, merge.ID(), map[string]bool{split.ID(): true}))
		})

	t.Run("no token upstream is unreachable",
		func(t *testing.T) {
			require.False(t,
				reachesOccupied(a, merge.ID(), map[string]bool{}))
		})

	t.Run("a sibling branch's token is not on the path",
		func(t *testing.T) {
			require.False(t,
				reachesOccupied(a, merge.ID(), map[string]bool{b.ID(): true}))
		})

	t.Run("convergence is cycle-guarded (split visited once)",
		func(t *testing.T) {
			// merge's two backward paths (via a and via b) both reach split;
			// the second visit must be skipped, and the walk must terminate.
			require.True(t,
				reachesOccupied(merge, "no-stop", map[string]bool{split.ID(): true}))
			require.False(t,
				reachesOccupied(merge, "no-stop", map[string]bool{}))
		})

	t.Run("the join node itself is never traversed",
		func(t *testing.T) {
			// merge is the stop; even though it is occupied it must not count
			// (a path "through the join" does not make an upstream flow reachable).
			require.False(t,
				reachesOccupied(a, merge.ID(), map[string]bool{merge.ID(): true}))
		})
}

func newDiamondInstance(t *testing.T, p *process.Process) *Instance {
	t.Helper()

	s, err := snapshot.New(p)
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	// Start from a clean tracks set so each test controls exactly which tokens are live.
	inst.tracks = map[string]*track{}

	return inst
}

// TestCheckFlowsWith exercises the reachability subset logic (checkFlowsWith) over an explicit
// occupied-node snapshot — the same snapshot recheckJoin now builds from the loop-owned position
// view (SRD-028 §3.4), without the removed live inst.CheckFlows path.
func TestCheckFlowsWith(t *testing.T) {
	_ = data.CreateDefaultStates()

	_, split, _, _, merge := orDiamond(t)

	t.Run("branch never taken — no live token, none reachable",
		func(t *testing.T) {
			reachable, err := checkFlowsWith(merge, merge.Incoming(), map[string]bool{})
			require.NoError(t, err)
			require.Empty(t, reachable)
		})

	t.Run("nil flow skipped",
		func(t *testing.T) {
			flows := append([]*flow.SequenceFlow{nil}, merge.Incoming()...)

			reachable, err := checkFlowsWith(merge, flows, map[string]bool{})
			require.NoError(t, err)
			require.Empty(t, reachable) // nil entry skipped; no token → none reachable
		})

	t.Run("live token upstream — both branches reachable",
		func(t *testing.T) {
			// A token at split can still reach both a and b → both flows reachable.
			reachable, err := checkFlowsWith(
				merge, merge.Incoming(), map[string]bool{split.ID(): true})
			require.NoError(t, err)
			require.Len(t, reachable, 2)
		})
}
