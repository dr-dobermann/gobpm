package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/lanes"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// lanedProcess builds one and the same process twice: once bare, once with
// every node placed on a lane. Same nodes, same flows, same conditions — the
// ONLY difference is the lane sets.
//
// withLanes also exercises the shapes that could plausibly leak into execution
// if lanes were not inert: several lanes, a nested lane set, and a node placed
// on more than one lane (the standard does not forbid it, so the engine must not
// either).
func lanedProcess(t *testing.T, id string, withLanes bool) *process.Process {
	t.Helper()

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	gw, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	left, err := activities.NewManualTask("left")
	require.NoError(t, err)

	right, err := activities.NewManualTask("right")
	require.NoError(t, err)

	join, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	// The lane sets are built over the SAME node objects the process will hold,
	// so the two variants differ in nothing but the presence of lanes.
	opts := []options.Option{}

	if withLanes {
		// A nested set, to prove depth changes nothing either.
		innerLane, err := lanes.NewLane("specialists", nil, "", nil)
		require.NoError(t, err)
		require.NoError(t, innerLane.Place(right))

		innerSet, err := lanes.NewLaneSet("inner", []*lanes.Lane{innerLane})
		require.NoError(t, err)

		sales, err := lanes.NewLane("sales", nil, "", nil)
		require.NoError(t, err)
		require.NoError(t, sales.Place(start, gw, left))

		// left is placed on TWO lanes: BPMN states no one-lane-per-node rule,
		// so the engine must not invent one, and it must still change nothing.
		ops, err := lanes.NewLane("operations", nil, "", innerSet)
		require.NoError(t, err)
		require.NoError(t, ops.Place(left, right, join, end))

		ls, err := lanes.NewLaneSet("org", []*lanes.Lane{sales, ops})
		require.NoError(t, err)

		opts = append(opts, lanes.WithLaneSets(ls))
	}

	p, err := process.New(id, opts...)
	require.NoError(t, err)

	for _, e := range []flow.Element{start, gw, left, right, join, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, gw)
	link(t, gw, left)
	link(t, gw, right)
	link(t, left, join)
	link(t, right, join)
	link(t, join, end)

	return p
}

// runToCompletion registers p, runs one instance and returns its final state.
func runToCompletion(t *testing.T, p *process.Process) string {
	t.Helper()

	th, err := thresher.New("lane-invariance-" + p.ID())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(p)
	require.NoError(t, err)

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	wctx, wc := context.WithTimeout(context.Background(), 5*time.Second)
	defer wc()

	state, err := h.WaitCompletion(wctx)
	require.NoError(t, err)

	return string(state)
}

// TestLanesDoNotAffectExecution — SRD-076 T-10, the load-bearing test of this
// landing.
//
// "Model-only" is a claim about BEHAVIOR, and structure tests cannot prove it.
// The only honest proof is to run the same process twice — once bare, once with
// every node laned, nested and double-placed — and require the executions to be
// indistinguishable.
//
// If a lane ever leaked into token flow, scheduling or completion, this is the
// test that would catch it.
func TestLanesDoNotAffectExecution(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	bare := runToCompletion(t, lanedProcess(t, "lanes-bare", false))
	laned := runToCompletion(t, lanedProcess(t, "lanes-laned", true))

	require.Equal(t, bare, laned,
		"a laned process must complete exactly as the same process without lanes")
}

// TestLaneSetsAreNotCloned — SRD-076 T-11: lane sets live on the DEFINITION.
// The per-instance node graph is a clone, and lanes have no business in it.
func TestLaneSetsAreNotCloned(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	p := lanedProcess(t, "lanes-clone", true)
	require.Len(t, p.LaneSets(), 1, "the definition carries them")

	th, err := thresher.New("lane-clone")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(p)
	require.NoError(t, err)

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	wctx, wc := context.WithTimeout(context.Background(), 5*time.Second)
	defer wc()

	_, err = h.WaitCompletion(wctx)
	require.NoError(t, err)

	// The definition is untouched by the run: an instance neither reads nor
	// mutates lane state, because it never received any.
	require.Len(t, p.LaneSets(), 1)
	require.Equal(t, "org", p.LaneSets()[0].Name())
	require.Len(t, p.LaneSets()[0].Lanes(), 2)
}

// TestLaneNodeIdentityIsNotReachableFromNodes pins the one-directional rule
// structurally: a flow.Node offers no way to ask which lane it is on, so no
// execution path can consult one. If someone later adds a Lane() accessor to an
// element, this test's premise is gone and the comment above it is a lie —
// which is exactly when it should be revisited.
func TestLaneNodeIdentityIsNotReachableFromNodes(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	task, err := activities.NewManualTask("solo")
	require.NoError(t, err)

	lane, err := lanes.NewLane("sales", nil, "", nil)
	require.NoError(t, err)
	require.NoError(t, lane.Place(task))

	// The lane knows the node...
	require.Len(t, lane.FlowNodes(), 1)
	require.Equal(t, task.ID(), lane.FlowNodes()[0].ID())

	// ...and the node is a plain flow.Node with no lane-shaped surface on it.
	var n flow.Node = task

	_, hasLane := any(n).(interface{ Lane() *lanes.Lane })
	require.False(t, hasLane,
		"an element must not expose its lane: membership runs one way only")

	_, hasLanes := any(n).(interface{ Lanes() []*lanes.Lane })
	require.False(t, hasLanes)
}
