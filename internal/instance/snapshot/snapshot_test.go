package snapshot_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/stretchr/testify/require"
)

func TestSnapshot(t *testing.T) {
	// nil process
	_, err := snapshot.New(nil)
	require.Error(t, err)

	p, err := process.New("test")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	task, err := activities.NewServiceTask(
		"task1",
		service.MustOperation("test_op", nil, nil, nil),
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")

	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add((task)))
	require.NoError(t, p.Add(end))

	_, err = flow.Link(start, task)
	require.NoError(t, err)

	_, err = flow.Link(task, end)
	require.NoError(t, err)

	_, err = snapshot.New(p)
	require.NoError(t, err)
}

// TestSnapshotNewRejectsMalformed covers the registration-time Process.Validate
// gate in snapshot.New: a process whose sequence flow connects nodes that are
// not in the process is rejected before a snapshot is built.
func TestSnapshotNewRejectsMalformed(t *testing.T) {
	p, err := process.New("malformed")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	// the flow's endpoints are never added to the process, so Validate must
	// reject it and snapshot.New must surface that error.
	f, err := flow.Link(start, end)
	require.NoError(t, err)
	require.NoError(t, p.Add(f))

	_, err = snapshot.New(p)
	require.Error(t, err)
}

func TestSnapshotClone(t *testing.T) {
	p, err := process.New("clone-src")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	gw, err := gateways.NewExclusiveGateway()
	require.NoError(t, err)

	end1, err := events.NewEndEvent("end1")
	require.NoError(t, err)

	end2, err := events.NewEndEvent("end2")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, gw, end1, end2} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, gw)
	require.NoError(t, err)

	dflow, err := flow.Link(gw, end1)
	require.NoError(t, err)

	_, err = flow.Link(gw, end2)
	require.NoError(t, err)

	require.NoError(t, gw.UpdateDefaultFlow(dflow))

	s, err := snapshot.New(p)
	require.NoError(t, err)

	clone, err := s.Clone()
	require.NoError(t, err)

	// independent snapshot, shared immutable header.
	require.NotSame(t, s, clone)
	require.Equal(t, s.ProcessID, clone.ProcessID)
	require.Equal(t, s.ProcessName, clone.ProcessName)

	// every node is a distinct clone keyed by the same id.
	require.Len(t, clone.Nodes, len(s.Nodes))
	for id, orig := range s.Nodes {
		cn, ok := clone.Nodes[id]
		require.True(t, ok, "node %q present in clone", id)
		require.NotSame(t, orig, cn, "node %q must be cloned", id)
		require.Equal(t, orig.ID(), cn.ID())
	}

	// flows are relinked between the clones (same ids, endpoints are clones).
	require.Len(t, clone.Flows, len(s.Flows))
	for id, of := range s.Flows {
		cf, ok := clone.Flows[id]
		require.True(t, ok, "flow %q present in clone", id)
		require.NotSame(t, of, cf, "flow %q must be cloned", id)
		require.Same(t, clone.Nodes[of.Source().ID()], cf.Source(),
			"flow %q source rewired to the clone", id)
		require.Same(t, clone.Nodes[of.Target().ID()], cf.Target(),
			"flow %q target rewired to the clone", id)
	}

	// the gateway's default flow is remapped onto the cloned edge.
	cgw, ok := clone.Nodes[gw.ID()].(flow.DefaultFlowHolder)
	require.True(t, ok)
	require.NotNil(t, cgw.DefaultFlow())
	require.Equal(t, dflow.ID(), cgw.DefaultFlow().ID())
	require.NotSame(t, gw.DefaultFlow(), cgw.DefaultFlow())
	require.Same(t, clone.Flows[dflow.ID()], cgw.DefaultFlow())
}

// TestSnapshotCloneGatewayWithoutDefault covers the remap step when a gateway
// carries no default flow: cloning leaves its default flow nil.
func TestSnapshotCloneGatewayWithoutDefault(t *testing.T) {
	p, err := process.New("clone-nodefault")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	gw, err := gateways.NewExclusiveGateway()
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, gw, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, gw)
	require.NoError(t, err)

	_, err = flow.Link(gw, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	clone, err := s.Clone()
	require.NoError(t, err)

	cgw, ok := clone.Nodes[gw.ID()].(flow.DefaultFlowHolder)
	require.True(t, ok)
	require.Nil(t, cgw.DefaultFlow())
}

// TestSnapshotCloneMalformed covers the two type-assertion guards in
// Snapshot.Clone: when a flow's source or target node is absent from the Nodes
// map, the lookup yields a nil node whose two-value assertion to
// SequenceSource/SequenceTarget reports ok=false and Clone returns an error.
func TestSnapshotCloneMalformed(t *testing.T) {
	build := func(t *testing.T) (s *snapshot.Snapshot, startID, endID string) {
		t.Helper()

		p, err := process.New("malformed-src")
		require.NoError(t, err)

		start, err := events.NewStartEvent("start")
		require.NoError(t, err)

		task, err := activities.NewServiceTask(
			"task1",
			service.MustOperation("test_op", nil, nil, nil),
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

		s, err = snapshot.New(p)
		require.NoError(t, err)

		return s, start.ID(), end.ID()
	}

	// nodesExcept returns a copy of s.Nodes with the node id omitted.
	nodesExcept := func(s *snapshot.Snapshot, id string) map[string]flow.Node {
		nodes := make(map[string]flow.Node, len(s.Nodes))
		for k, v := range s.Nodes {
			if k == id {
				continue
			}

			nodes[k] = v
		}

		return nodes
	}

	t.Run("missing target node hits the target guard",
		func(t *testing.T) {
			s, _, endID := build(t)

			// omit the end: the task->end flow's source (task) is still
			// present, so only its target lookup fails, hitting the trg guard.
			bad := &snapshot.Snapshot{
				ProcessID:   s.ProcessID,
				ProcessName: s.ProcessName,
				Nodes:       nodesExcept(s, endID),
				Flows:       s.Flows,
				Properties:  nil,
			}

			_, err := bad.Clone()
			require.Error(t, err)
		})

	t.Run("missing source node hits the source guard",
		func(t *testing.T) {
			s, startID, _ := build(t)

			// omit the start: the start->task flow's source is now absent.
			bad := &snapshot.Snapshot{
				ProcessID:   s.ProcessID,
				ProcessName: s.ProcessName,
				Nodes:       nodesExcept(s, startID),
				Flows:       s.Flows,
				Properties:  nil,
			}

			_, err := bad.Clone()
			require.Error(t, err)
		})
}
