package instance

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// SRD-049 M4 — the scope-aware runtime: open/seed/park/drain/close/resume.

// hitTask counts executions; put != "" additionally Puts a local datum.
func hitTask(
	t *testing.T, name string, hits *atomic.Int32, put string, val int,
) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			hits.Add(1)

			if put == "" {
				return nil, nil
			}

			return data.MustItemDefinition(
				values.NewVariable(val), foundation.WithID(put)), nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(name, op, activities.WithoutParams())
	require.NoError(t, err)

	return st
}

// runInstance builds the snapshot, runs the instance, and waits for the
// terminal state.
func runInstance(t *testing.T, p *process.Process) *Instance {
	t.Helper()

	s, err := snapshot.New(p)
	require.NoError(t, err)

	// the tolerant producer: hub registrations (a ReceiveTask, a boundary
	// watch) succeed silently — these tests assert loop behavior, not hub
	// traffic.
	ep := &capturingProducer{procs: map[string]eventproc.EventProcessor{}}

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, inst.Run(ctx))

	require.Eventually(t,
		func() bool {
			st := inst.State()

			return st == Completed || st == Terminated
		},
		3*time.Second, 5*time.Millisecond)

	return inst
}

// linkAll links the pairs in order.
func linkAll(t *testing.T, pairs ...[2]flow.Element) {
	t.Helper()

	for _, pr := range pairs {
		_, err := flow.Link(
			pr[0].(flow.SequenceSource), pr[1].(flow.SequenceTarget))
		require.NoError(t, err)
	}
}

// wrapSP builds a process: start → sp → after → end.
func wrapSP(
	t *testing.T, name string, sp *activities.SubProcess,
	after *activities.ServiceTask,
) *process.Process {
	t.Helper()

	p, err := process.New(name)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, sp, after, end} {
		require.NoError(t, p.Add(e))
	}

	linkAll(t, [2]flow.Element{start, sp}, [2]flow.Element{sp, after},
		[2]flow.Element{after, end})

	return p
}

// TestScopeOpenSeedsNoneStart — the unique-None-start shape seeds, the
// host parks, the drain resumes it onto its outgoing (SRD-049 FR-8/FR-9).
func TestScopeOpenSeedsNoneStart(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var inner, outer atomic.Int32

	sp, err := activities.NewSubProcess("body")
	require.NoError(t, err)

	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	task := hitTask(t, "inner", &inner, "", 0)
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{sStart, task, sEnd} {
		require.NoError(t, sp.Add(e))
	}
	linkAll(t, [2]flow.Element{sStart, task}, [2]flow.Element{task, sEnd})

	after := hitTask(t, "after", &outer, "", 0)

	inst := runInstance(t, wrapSP(t, "seed-none", sp, after))

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, inner.Load(), "the inner task must run")
	require.EqualValues(t, 1, outer.Load(), "the host must resume onto after")
	require.Empty(t, inst.tracks[""], "sanity")
}

// TestScopeOpenSeedsFlowlessNodes — the no-start shape seeds every
// flow-less inner activity (§13.3.4's second normative shape).
func TestScopeOpenSeedsFlowlessNodes(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var a, b, outer atomic.Int32

	sp, err := activities.NewSubProcess("body")
	require.NoError(t, err)

	ta := hitTask(t, "a", &a, "", 0)
	tb := hitTask(t, "b", &b, "", 0)
	ea, err := events.NewEndEvent("ea")
	require.NoError(t, err)
	eb, err := events.NewEndEvent("eb")
	require.NoError(t, err)

	for _, e := range []flow.Element{ta, tb, ea, eb} {
		require.NoError(t, sp.Add(e))
	}
	linkAll(t, [2]flow.Element{ta, ea}, [2]flow.Element{tb, eb})

	after := hitTask(t, "after", &outer, "", 0)

	inst := runInstance(t, wrapSP(t, "seed-flowless", sp, after))

	require.Equal(t, Completed, inst.State())
	require.EqualValues(t, 1, a.Load())
	require.EqualValues(t, 1, b.Load())
	require.EqualValues(t, 1, outer.Load(),
		"the host resumes only after BOTH seeds drained")
}

// TestScopeDataVisibility — an inner task reads parent data via the
// walk-up, and its Put lands in the child scope, disposed at close
// (§10.5.7; SRD-049 FR-7).
func TestScopeDataVisibility(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var sawTotal atomic.Int32

	op, err := gooper.New("reader",
		func(ctx context.Context, ds service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := ds.GetData("total")
			if err != nil {
				return nil, err
			}

			if v, _ := d.Value().Get(ctx).(int); v == 42 {
				sawTotal.Add(1)
			}

			// a local: must land in the CHILD scope and die with it.
			return data.MustItemDefinition(
				values.NewVariable(7), foundation.WithID("temp")), nil
		})
	require.NoError(t, err)

	reader, err := activities.NewServiceTask("reader", op,
		activities.WithoutParams())
	require.NoError(t, err)

	sp, err := activities.NewSubProcess("body")
	require.NoError(t, err)

	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{sStart, reader, sEnd} {
		require.NoError(t, sp.Add(e))
	}
	linkAll(t, [2]flow.Element{sStart, reader}, [2]flow.Element{reader, sEnd})

	var outer atomic.Int32
	after := hitTask(t, "after", &outer, "", 0)

	p2, err := process.New("visibility-prop",
		data.WithProperties(
			data.MustProperty("total",
				data.MustItemDefinition(values.NewVariable(42),
					foundation.WithID("total")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, sp, after, end} {
		require.NoError(t, p2.Add(e))
	}
	linkAll(t, [2]flow.Element{start, sp}, [2]flow.Element{sp, after},
		[2]flow.Element{after, end})

	inst := runInstance(t, p2)

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, sawTotal.Load(),
		"the inner read must see the parent property via the walk-up")

	// the inner local is gone with its scope: not at the root.
	_, err = inst.sc.plane.GetDataByID(inst.sc.root, "temp")
	require.Error(t, err, "the child-scope local must not leak to the root")
}

// TestNestedScopes — two container levels drain inside-out.
func TestNestedScopes(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var leaf, outer atomic.Int32

	innerSP, err := activities.NewSubProcess("inner")
	require.NoError(t, err)

	iStart, err := events.NewStartEvent("i-start")
	require.NoError(t, err)
	lt := hitTask(t, "leaf", &leaf, "", 0)
	iEnd, err := events.NewEndEvent("i-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{iStart, lt, iEnd} {
		require.NoError(t, innerSP.Add(e))
	}
	linkAll(t, [2]flow.Element{iStart, lt}, [2]flow.Element{lt, iEnd})

	outerSP, err := activities.NewSubProcess("outer")
	require.NoError(t, err)

	oStart, err := events.NewStartEvent("o-start")
	require.NoError(t, err)
	oEnd, err := events.NewEndEvent("o-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{oStart, innerSP, oEnd} {
		require.NoError(t, outerSP.Add(e))
	}
	linkAll(t, [2]flow.Element{oStart, innerSP},
		[2]flow.Element{innerSP, oEnd})

	after := hitTask(t, "after", &outer, "", 0)

	inst := runInstance(t, wrapSP(t, "nested", outerSP, after))

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, leaf.Load())
	require.EqualValues(t, 1, outer.Load())
}

// TestScopeReEntryQueues — two parallel tokens entering the SAME composite
// serialize: one scope at a time, both complete (SRD-049 §4.4).
func TestScopeReEntryQueues(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var inner atomic.Int32

	sp, err := activities.NewSubProcess("body")
	require.NoError(t, err)

	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	task := hitTask(t, "inner", &inner, "", 0)
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{sStart, task, sEnd} {
		require.NoError(t, sp.Add(e))
	}
	linkAll(t, [2]flow.Element{sStart, task}, [2]flow.Element{task, sEnd})

	p, err := process.New("re-entry")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	fork, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)
	lead := hitTask(t, "lead", &atomic.Int32{}, "", 0)
	endA, err := events.NewEndEvent("endA")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, fork, lead, sp, endA} {
		require.NoError(t, p.Add(e))
	}

	// both branches converge INTO the same sub-process node: fork → sp and
	// fork → lead → sp. Two tokens, one composite.
	linkAll(t,
		[2]flow.Element{start, fork},
		[2]flow.Element{fork, sp},
		[2]flow.Element{fork, lead},
		[2]flow.Element{lead, sp},
		[2]flow.Element{sp, endA})

	inst := runInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 2, inner.Load(),
		"both activations must run the body (serialized re-entry)")
}

// TestConditionalInsideScope — a conditional catch inside a sub-process
// evaluates at ITS scope (walk-up to the root property) and is released by
// an outer commit (SRD-049 FR-7 + the SRD-048 machinery).
func TestConditionalInsideScope(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	cond, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "total")
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(int)

			return values.NewVariable(v > 100), nil
		},
		goexpr.WithDependencies("total"))
	require.NoError(t, err)

	watch, err := events.NewIntermediateCatchEvent("watch",
		events.MustConditionalEventDefinition(cond))
	require.NoError(t, err)

	sp, err := activities.NewSubProcess("body")
	require.NoError(t, err)

	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{sStart, watch, sEnd} {
		require.NoError(t, sp.Add(e))
	}
	linkAll(t, [2]flow.Element{sStart, watch}, [2]flow.Element{watch, sEnd})

	p, err := process.New("cond-in-scope",
		data.WithProperties(
			data.MustProperty("total",
				data.MustItemDefinition(values.NewVariable(10),
					foundation.WithID("total")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	fork, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	var raised atomic.Int32
	raise := hitTask(t, "raise", &raised, "total", 150)

	endR, err := events.NewEndEvent("endR")
	require.NoError(t, err)
	endS, err := events.NewEndEvent("endS")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, fork, raise, sp, endR, endS} {
		require.NoError(t, p.Add(e))
	}
	linkAll(t,
		[2]flow.Element{start, fork},
		[2]flow.Element{fork, raise},
		[2]flow.Element{fork, sp},
		[2]flow.Element{raise, endR},
		[2]flow.Element{sp, endS})

	inst := runInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, raised.Load())
}

// TestScopeDoneSentinel — the synthetic completion's minimal surface.
func TestScopeDoneSentinel(t *testing.T) {
	sd := newScopeDone()
	require.Equal(t, scopeDoneTrigger, sd.Type())
	require.Nil(t, sd.GetItemsList())
	require.NotEmpty(t, sd.ID())
}

// TestScopeRuntimeDirect — the deterministic direct-drive of the branches
// integration timing can't pin: queueing, reopen, and the failure paths.
func TestScopeRuntimeDirect(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// a parked-host fixture: instance with one composite, un-run loop.
	build := func(t *testing.T) (*Instance, *loopState, *track, flow.Node) {
		t.Helper()

		sp, err := activities.NewSubProcess("body")
		require.NoError(t, err)
		ss, err := events.NewStartEvent("s")
		require.NoError(t, err)
		se, err := events.NewEndEvent("e")
		require.NoError(t, err)
		require.NoError(t, sp.Add(ss))
		require.NoError(t, sp.Add(se))
		linkAll(t, [2]flow.Element{ss, se})

		p, err := process.New("direct")
		require.NoError(t, err)
		start, err := events.NewStartEvent("start")
		require.NoError(t, err)
		end, err := events.NewEndEvent("end")
		require.NoError(t, err)
		for _, e := range []flow.Element{start, sp, end} {
			require.NoError(t, p.Add(e))
		}
		linkAll(t, [2]flow.Element{start, sp}, [2]flow.Element{sp, end})

		s, err := snapshot.New(p)
		require.NoError(t, err)

		inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
			mockeventproc.NewMockEventProducer(t), nil)
		require.NoError(t, err)
		inst.tracks = map[string]*track{}

		var node flow.Node
		for _, n := range inst.s.Nodes {
			if _, ok := n.(scopeHost); ok {
				node = n
			}
		}
		require.NotNil(t, node)

		host, err := newTrack(node, inst, nil)
		require.NoError(t, err)

		ls := newLoopState(inst)
		ls.position[host.ID()] = node

		return inst, ls, host, node
	}

	t.Run("stopping drops the open", func(t *testing.T) {
		_, ls, host, node := build(t)
		ls.stopping = true

		ls.onScopeOpen(t.Context(), host, node)
		require.Empty(t, ls.scopes)
	})

	t.Run("non-composite node faults", func(t *testing.T) {
		inst, ls, host, _ := build(t)

		plain, err := events.NewEndEvent("plain")
		require.NoError(t, err)

		ls.onScopeOpen(t.Context(), host, plain)
		require.True(t, ls.stopping)
		require.Error(t, inst.LastErr())
	})

	t.Run("queue and reopen are deterministic", func(t *testing.T) {
		inst, ls, host, node := build(t)
		ctx := t.Context()

		ls.onScopeOpen(ctx, host, node)
		require.Len(t, ls.scopes, 1)

		// a second host on the same composite queues.
		host2, err := newTrack(node, inst, nil)
		require.NoError(t, err)

		ls.onScopeOpen(ctx, host2, node)
		require.Len(t, ls.scopes, 1, "one open scope per composite")

		var path scope.DataPath
		var entry *scopeEntry
		for p, e := range ls.scopes {
			path, entry = p, e
		}
		require.Len(t, entry.queue, 1)

		// drain the seeded inner tracks' accounting by force-completing.
		entry.active = 0
		ls.completeScope(ctx, path, entry)

		// the queued host reopened the scope.
		require.Len(t, ls.scopes, 1)
		for _, e := range ls.scopes {
			require.Same(t, host2, e.host)
		}
	})

	t.Run("two queued hosts carry over reopens", func(t *testing.T) {
		inst, ls, host, node := build(t)
		ctx := t.Context()

		ls.onScopeOpen(ctx, host, node)

		h2, err := newTrack(node, inst, nil)
		require.NoError(t, err)
		h3, err := newTrack(node, inst, nil)
		require.NoError(t, err)

		ls.onScopeOpen(ctx, h2, node)
		ls.onScopeOpen(ctx, h3, node)

		var path scope.DataPath
		var entry *scopeEntry
		for p, e := range ls.scopes {
			path, entry = p, e
		}
		require.Len(t, entry.queue, 2)

		entry.active = 0
		ls.completeScope(ctx, path, entry)

		// h2 reopened; h3 carried into the fresh entry's queue.
		fresh, ok := ls.scopes[path]
		require.True(t, ok)
		require.Same(t, h2, fresh.host)
		require.Len(t, fresh.queue, 1)
		require.Same(t, h3, fresh.queue[0])
	})

	t.Run("born-parked composite opens from spawn", func(t *testing.T) {
		_, ls, host, _ := build(t)

		// spawn runs recordBornWaiter, which opens the scope for a track
		// born parked ON a composite (a fork straight into a sub-process).
		ls.spawn(t.Context(), host)

		require.Len(t, ls.scopes, 1)
	})

	t.Run("late scope terminate is a no-op", func(t *testing.T) {
		_, ls, host, _ := build(t)

		ls.terminateScope(t.Context(), host.scopePath) // nothing open
		require.False(t, ls.stopping)
	})

	t.Run("close failure faults", func(t *testing.T) {
		inst, ls, host, node := build(t)
		ctx := t.Context()

		ls.onScopeOpen(ctx, host, node)

		var path scope.DataPath
		var entry *scopeEntry
		for p, e := range ls.scopes {
			path, entry = p, e
		}

		// an open grandchild blocks the close — the corrupt-tree branch.
		grand, err := path.Append("stuck")
		require.NoError(t, err)
		require.NoError(t, inst.sc.plane.OpenScope(grand))

		entry.active = 0
		ls.completeScope(ctx, path, entry)

		require.True(t, ls.stopping)
		require.Error(t, inst.LastErr())
	})

	t.Run("data-plane open failure faults", func(t *testing.T) {
		inst, ls, host, node := build(t)
		ctx := t.Context()

		// pre-open the child path — onScopeOpen's OpenScope then duplicates.
		child, err := host.scopePath.Append(scopeSegment(node))
		require.NoError(t, err)
		require.NoError(t, inst.sc.plane.OpenScope(child))

		ls.onScopeOpen(ctx, host, node)

		require.True(t, ls.stopping)
		require.Error(t, inst.LastErr())
	})

	t.Run("seed build failure faults", func(t *testing.T) {
		inst, ls, host, node := build(t)
		ctx := t.Context()

		child, err := host.scopePath.Append("bad-seed")
		require.NoError(t, err)
		require.NoError(t, inst.sc.plane.OpenScope(child))
		ls.scopes[child] = &scopeEntry{host: host, node: node}

		// a non-executor node (the executenode_test pattern, plus a quiet
		// NodeType so the seed filter passes) — newTrack rejects it.
		bn, err := flow.NewBaseNode("plain")
		require.NoError(t, err)

		ls.seedScope(ctx, badHost{node.(scopeHost), nonExecNode{bn}}, child)

		require.True(t, ls.stopping)
		require.Error(t, inst.LastErr())
	})

	t.Run("seed under stopping stops the seeds", func(t *testing.T) {
		_, ls, host, node := build(t)
		ctx := t.Context()

		// stop AFTER the open guard: flip stopping between open and seed by
		// seeding directly.
		sh := node.(scopeHost)
		child, err := host.scopePath.Append(scopeSegment(node))
		require.NoError(t, err)
		require.NoError(t, ls.inst.sc.plane.OpenScope(child))
		ls.scopes[child] = &scopeEntry{host: host, node: node}

		ls.stopping = true
		ls.seedScope(ctx, sh, child)

		for _, tr := range ls.inst.tracks {
			require.True(t, tr.stopIt.Load(),
				"a seed spawned under stopping must be stopped")
		}
	})
}

// nonExecNode is a flow.Node without exec.NodeExecutor whose NodeType is
// quiet (the bare BaseNode's panics), so it passes the seed filter and
// fails at newTrack.
type nonExecNode struct{ *flow.BaseNode }

// NodeType reports an activity so the flow-less seed filter accepts it.
func (n nonExecNode) NodeType() flow.NodeType { return flow.ActivityNodeType }

// badHost wraps a real composite but seeds a non-executor node — the
// seed-build failure fixture.
type badHost struct {
	scopeHost

	bad flow.Node
}

// Nodes returns the single non-executor seed.
func (b badHost) Nodes() []flow.Node { return []flow.Node{b.bad} }
