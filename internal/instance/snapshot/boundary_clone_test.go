package snapshot_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// TestSnapshotClonesAndRebindsBoundary (SRD-029 M3a): the per-instance graph
// build carries an activity's boundary event and rebinds both cross-references
// (host→boundary and boundary→host) onto the instance's own cloned nodes, and
// relinks the boundary's exception flow to the cloned target.
func TestSnapshotClonesAndRebindsBoundary(t *testing.T) {
	data.CreateDefaultStates()

	p, err := process.New("boundary-clone")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	msg := bpmncommon.MustMessage("m",
		data.MustItemDefinition(values.NewVariable(1)))

	task, err := activities.NewReceiveTask("task", msg)
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	excEnd, err := events.NewEndEvent("exc")
	require.NoError(t, err)

	sig, err := events.NewSignal("s", nil)
	require.NoError(t, err)

	sigDef, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	be, err := events.NewBoundaryEvent("bnd", task, sigDef, true)
	require.NoError(t, err)

	for _, e := range []flow.Element{start, task, end, excEnd, be} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, task)
	require.NoError(t, err)

	_, err = flow.Link(task, end)
	require.NoError(t, err)

	// the boundary's outgoing (exception) flow.
	_, err = flow.Link(be, excEnd)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	// the model boundary is captured as a node.
	require.Contains(t, s.Nodes, be.ID())

	clone, err := s.Clone()
	require.NoError(t, err)

	// the cloned host carries exactly the cloned boundary.
	cloneTask := clone.Nodes[task.ID()]
	bh, ok := cloneTask.(interface {
		BoundaryEvents() []flow.EventNode
	})
	require.True(t, ok, "cloned host exposes BoundaryEvents()")

	bes := bh.BoundaryEvents()
	require.Len(t, bes, 1)
	require.Same(t, clone.Nodes[be.ID()], bes[0],
		"the host's boundary is the cloned graph node")

	cloneBE, ok := bes[0].(flow.BoundaryEvent)
	require.True(t, ok)
	require.Equal(t, be.ID(), cloneBE.ID())
	require.NotSame(t, be, cloneBE, "the boundary is a per-instance clone")

	// boundary→host back-reference points at the CLONED host, not the model.
	require.Equal(t, task.ID(), cloneBE.AttachedTo().ID())
	require.NotSame(t, task, cloneBE.AttachedTo())

	// the exception flow is relinked to the cloned target.
	require.Len(t, cloneBE.Outgoing(), 1)
	require.Same(t, clone.Nodes[excEnd.ID()],
		cloneBE.Outgoing()[0].Target().Node(),
		"the exception flow targets the cloned node")
}
