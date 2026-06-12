package instance

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/stretchr/testify/require"
)

// buildPlainSnapshot builds a minimal executable process: start -> task -> end.
// Executing any node writes per-node runtime state (dataPath via RegisterData,
// operation message carriers), so two instances running over a SHARED node
// graph would race on it.
func buildPlainSnapshot(t *testing.T) *snapshot.Snapshot {
	t.Helper()

	p, err := process.New("clone-race")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	task, err := activities.NewServiceTask(
		"task1",
		service.MustOperation("op", nil, nil, nil),
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, task, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, task)
	require.NoError(t, err)

	_, err = flow.Link(task, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	return s
}

// TestCloneRaceTwoInstances runs two instances built from ONE shared snapshot
// concurrently. With the per-instance node graph (ADR-009) each instance owns a
// private clone of every node, so the previously shared per-node runtime state
// (dataPath, scope, operation messages) is never written from two goroutines at
// once — the race detector stays clean and both instances complete.
func TestCloneRaceTwoInstances(t *testing.T) {
	_ = data.CreateDefaultStates()

	s := buildPlainSnapshot(t)

	inst1, err := New(s, scope.EmptyDataPath, enginert.Default(),
		mockeventproc.NewMockEventProducer(t), nil)
	require.NoError(t, err)

	inst2, err := New(s, scope.EmptyDataPath, enginert.Default(),
		mockeventproc.NewMockEventProducer(t), nil)
	require.NoError(t, err)

	// the two instances own independent clones of every node — and neither
	// shares a node with the template snapshot.
	for id, n1 := range inst1.s.Nodes {
		require.NotSame(t, n1, inst2.s.Nodes[id],
			"node %q must be per-instance", id)
		require.NotSame(t, n1, s.Nodes[id],
			"instance node %q must differ from the template", id)
	}

	leak := assertNoGoroutineLeak(t)
	defer leak()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run is non-blocking — both event loops run concurrently from here, each
	// executing its own node clones (the race detector watches this).
	require.NoError(t, inst1.Run(ctx))
	require.NoError(t, inst2.Run(ctx))

	for _, inst := range []*Instance{inst1, inst2} {
		require.Eventually(t,
			func() bool { return inst.State() == Completed },
			2*time.Second, 5*time.Millisecond,
			"both instances should complete")
		require.NoError(t, inst.LastErr())
	}
}
