package snapshot_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/stretchr/testify/require"
)

// SRD-049 M3 — snapshot recursion over nested sub-processes.

// nTask builds a minimal in-process service task.
func nTask(t *testing.T, name string) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return nil, nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(name, op, activities.WithoutParams())
	require.NoError(t, err)

	return st
}

// spBody fills sp with start → inner... → end; inner nodes are appended
// between them.
func spBody(t *testing.T, sp *activities.SubProcess, inner ...flow.Node) {
	t.Helper()

	start, err := events.NewStartEvent("start-" + sp.Name())
	require.NoError(t, err)
	end, err := events.NewEndEvent("end-" + sp.Name())
	require.NoError(t, err)

	require.NoError(t, sp.Add(start))
	require.NoError(t, sp.Add(end))

	prev := flow.Node(start)
	for _, n := range inner {
		require.NoError(t, sp.Add(n))
		_, err = flow.Link(prev.(flow.SequenceSource), n.(flow.SequenceTarget))
		require.NoError(t, err)
		prev = n
	}

	_, err = flow.Link(prev.(flow.SequenceSource), end)
	require.NoError(t, err)
}

// nestedProcess builds: start → outer[ start → inner[ start → task → end ]
// → end ] → end — two container levels.
func nestedProcess(t *testing.T, name string) (*process.Process, *activities.SubProcess) {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	inner, err := activities.NewSubProcess("inner")
	require.NoError(t, err)
	spBody(t, inner, nTask(t, "leaf"))

	outer, err := activities.NewSubProcess("outer")
	require.NoError(t, err)
	spBody(t, outer, inner)

	p, err := process.New(name)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, outer, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, outer)
	require.NoError(t, err)
	_, err = flow.Link(outer, end)
	require.NoError(t, err)

	return p, outer
}

// findSP resolves a SubProcess by name from a node set.
func findSP(t *testing.T, nodes map[string]flow.Node, name string) *activities.SubProcess {
	t.Helper()

	for _, n := range nodes {
		if sp, ok := n.(*activities.SubProcess); ok && sp.Name() == name {
			return sp
		}
	}

	t.Fatalf("sub-process %q not found", name)

	return nil
}

// innerByName resolves an inner node of a SubProcess by name.
func innerByName(t *testing.T, sp *activities.SubProcess, name string) flow.Node {
	t.Helper()

	for _, n := range sp.Nodes() {
		if n.Name() == name {
			return n
		}
	}

	t.Fatalf("inner node %q not found in %q", name, sp.Name())

	return nil
}

// TestSnapshotNested — a nested definition snapshots and clones with every
// level disjoint (the Clone-drop bug class, SRD-049 FR-6).
func TestSnapshotNested(t *testing.T) {
	p, srcOuter := nestedProcess(t, "nested")

	s, err := snapshot.New(p)
	require.NoError(t, err)

	snapOuter := findSP(t, s.Nodes, "outer")
	require.NotSame(t, srcOuter, snapOuter,
		"the snapshot's outer sub-process is a clone")

	c1, err := s.Clone()
	require.NoError(t, err)
	c2, err := s.Clone()
	require.NoError(t, err)

	o1, o2 := findSP(t, c1.Nodes, "outer"), findSP(t, c2.Nodes, "outer")
	require.NotSame(t, o1, o2)
	require.NotSame(t, snapOuter, o1)

	i1 := innerByName(t, o1, "inner").(*activities.SubProcess)
	i2 := innerByName(t, o2, "inner").(*activities.SubProcess)
	require.NotSame(t, i1, i2, "the nested level is disjoint per instance")

	l1, l2 := innerByName(t, i1, "leaf"), innerByName(t, i2, "leaf")
	require.NotSame(t, l1, l2, "the deepest nodes are disjoint per instance")
}

// TestHasConditionalsDeep — a conditional catch two levels down flips the
// precomputed flag, and Clone carries it (SRD-049 FR-5).
func TestHasConditionalsDeep(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	cond, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(true), nil
		})
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("watch",
		events.MustConditionalEventDefinition(cond))
	require.NoError(t, err)

	inner, err := activities.NewSubProcess("inner")
	require.NoError(t, err)
	spBody(t, inner, catch)

	outer, err := activities.NewSubProcess("outer")
	require.NoError(t, err)
	spBody(t, outer, inner)

	p, err := process.New("deep-cond")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, outer, end} {
		require.NoError(t, p.Add(e))
	}
	_, err = flow.Link(start, outer)
	require.NoError(t, err)
	_, err = flow.Link(outer, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)
	require.True(t, s.HasConditionals,
		"a nested conditional must flip the flag")

	c, err := s.Clone()
	require.NoError(t, err)
	require.True(t, c.HasConditionals, "Clone carries the deep flag")
}

// TestInstantiatingStartsTopLevelOnly — inner starts never become
// instantiation points (SRD-049 FR-5).
func TestInstantiatingStartsTopLevelOnly(t *testing.T) {
	p, _ := nestedProcess(t, "starts")

	s, err := snapshot.New(p)
	require.NoError(t, err)
	require.Empty(t, s.InstantiatingStarts,
		"a none-start process with nested none-starts has no "+
			"instantiating (message/signal) starts at any level")
}

// TestSnapshotRejectsInvalidInnerShape — a broken inner shape fails
// registration through the per-node Validate recursion.
func TestSnapshotRejectsInvalidInnerShape(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	empty, err := activities.NewSubProcess("empty") // no entry → invalid
	require.NoError(t, err)

	p, err := process.New("bad-inner")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, empty, end} {
		require.NoError(t, p.Add(e))
	}
	_, err = flow.Link(start, empty)
	require.NoError(t, err)
	_, err = flow.Link(empty, end)
	require.NoError(t, err)

	_, err = snapshot.New(p)
	require.Error(t, err)
	require.Contains(t, err.Error(), "needs an entry")
}
