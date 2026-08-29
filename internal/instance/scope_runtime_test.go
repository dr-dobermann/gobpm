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

// runIteration builds the snapshot, runs the instance, and waits for the
// terminal state.
func runIteration(t *testing.T, p *process.Process) *Instance {
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

			// an incident park is a settled outcome too (SRD-079 FR-1):
			// the instance stays Active with open incidents.
			return st == Completed || st == Terminated ||
				inst.OpenIncidents() > 0
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

	inst := runIteration(t, wrapSP(t, "seed-none", sp, after))

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

	inst := runIteration(t, wrapSP(t, "seed-flowless", sp, after))

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

	inst := runIteration(t, p2)

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

	inst := runIteration(t, wrapSP(t, "nested", outerSP, after))

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

	inst := runIteration(t, p)

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

	inst := runIteration(t, p)

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
// scopeQueueFixture builds a plain composite and a host track standing on
// it, with a loop state that is NOT running — the white-box pieces the
// scope-open and re-entry-queue tests drive directly.
func scopeQueueFixture(
	t *testing.T,
) (*Instance, *loopState, *track, flow.Node) {
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

// integration timing can't pin: queueing, reopen, and the failure paths.
func TestScopeRuntimeDirect(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// a parked-host fixture: iteration with one composite, un-run loop.
	build := scopeQueueFixture

	// T-1 findings, SRD-090.A M3c: the two below asserted that a bad open
	// FAULTS the instance. The loop-driven path had nobody to answer, so
	// stopAll was the only way to be loud. An open is a request now, and a
	// refusal goes back to the activity iteration that asked — which is how
	// every other error in this path already behaved.
	t.Run("stopping refuses the open", func(t *testing.T) {
		_, ls, host, node := build(t)
		ls.stopping = true

		reply, answered := openScopeFor(t.Context(), t, ls, host, node)

		require.True(t, answered)
		require.Error(t, reply.err)
		require.Empty(t, ls.scopes)
	})

	t.Run("non-composite node is refused", func(t *testing.T) {
		_, ls, host, _ := build(t)

		plain, err := events.NewEndEvent("plain")
		require.NoError(t, err)

		reply, answered := openScopeFor(t.Context(), t, ls, host, plain)

		require.True(t, answered)
		require.Error(t, reply.err)
		require.Empty(t, ls.scopes)
	})

	t.Run("queue and reopen are deterministic", func(t *testing.T) {
		inst, ls, host, node := build(t)
		ctx := t.Context()

		openScopeFor(ctx, t, ls, host, node)
		require.Len(t, ls.scopes, 1)

		// a second host on the same composite queues.
		host2, err := newTrack(node, inst, nil)
		require.NoError(t, err)

		openScopeFor(ctx, t, ls, host2, node)
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

		openScopeFor(ctx, t, ls, host, node)

		h2, err := newTrack(node, inst, nil)
		require.NoError(t, err)
		h3, err := newTrack(node, inst, nil)
		require.NoError(t, err)

		openScopeFor(ctx, t, ls, h2, node)
		openScopeFor(ctx, t, ls, h3, node)

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
		require.Same(t, h3, fresh.queue[0].host,
			"the queue holds REQUESTS now — each with its own deferred reply")
	})

	// T-1 finding, SRD-090.A M3c: "born-parked composite opens from spawn"
	// is deleted rather than rewritten. A track born on a composite is no
	// longer born PARKED at all, so the spawn path opens no scope: it starts
	// Ready, reaches its step on its own goroutine, and its executor asks
	// for the open through the ordinary roundtrip. The behaviour the subtest
	// pinned has no successor — the mechanism it existed for is gone.

	t.Run("late scope terminate is a no-op", func(t *testing.T) {
		_, ls, host, _ := build(t)

		ls.terminateScope(t.Context(), host.scopePath) // nothing open
		require.False(t, ls.stopping)
	})

	t.Run("close failure faults", func(t *testing.T) {
		inst, ls, host, node := build(t)
		ctx := t.Context()

		openScopeFor(ctx, t, ls, host, node)

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

		// pre-open the child path on the PLANE only, leaving the loop's
		// table empty: the open then duplicates rather than queueing.
		child, err := host.scopePath.Append(scopeSegment(node))
		require.NoError(t, err)
		require.NoError(t, inst.sc.plane.OpenScope(child))

		reply, answered := openScopeFor(ctx, t, ls, host, node)

		// T-1 finding, SRD-090.A M3c: was `ls.stopping` — see the two
		// refusals above for why a bad open answers instead of faulting.
		require.True(t, answered)
		require.Error(t, reply.err)
		require.Empty(t, ls.scopes)
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

// openScopeFor drives the surviving scope-open path the way an activity
// iteration's executor does, synchronously on the caller's goroutine
// (SRD-090.A M3c).
//
// It replaces the direct ls.onScopeOpen(...) these tests used before the
// loop-driven open path retired. The mechanism moved; the behaviour under
// test did not, which is why these are rewrites rather than deletions.
//
// The reply channel is BUFFERED so handleScopeOpen's send does not block
// with no runner parked on the other end, and the read is non-blocking: a
// request that lands on an already-open path is QUEUED and its reply
// deferred until the path frees, which is the one case with no answer yet.
func openScopeFor(
	ctx context.Context,
	t *testing.T,
	ls *loopState,
	host *track,
	node flow.Node,
) (scopeReply, bool) {
	t.Helper()

	req := scopeRequest{
		op:    scopeOpen,
		host:  host,
		node:  node,
		drain: make(chan struct{}),
		reply: make(chan scopeReply, 1),
	}

	ls.handleScopeOpen(ctx, req)

	select {
	case r := <-req.reply:
		return r, true
	default:
		return scopeReply{}, false
	}
}

// TestTwoHostsOneComposite (T-15, SRD-090.A M3c): two tokens reach the SAME
// Sub-Process concurrently — a parallel gateway forks into it — and both
// bodies run to completion.
//
// This is the case the scope re-entry queue exists for, and the reason it
// is the milestone's principal regression risk: one DataPath holds one
// scope, so the second host has to wait for the first, and M3c MOVED that
// queue from the retired loop-driven open path onto the executor's request
// path, where waiting means a DEFERRED REPLY rather than a parked track.
//
// It is asserted end to end rather than white-box because the white-box
// subtests pin the queue's SHAPE and would keep passing if the deferred
// reply never arrived: the host would simply hang, and a hang inside a
// 3-second helper reads as a timeout, not as a queue that stopped serving.
// Counting body runs is what distinguishes "both hosts got the scope" from
// "one host got it twice" and from "the second never woke".
func TestTwoHostsOneComposite(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var body atomic.Int32

	sub, err := activities.NewSubProcess("shared")
	require.NoError(t, err)

	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	inner := hitTask(t, "inner", &body, "", 0)
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{sStart, inner, sEnd} {
		require.NoError(t, sub.Add(e))
	}

	linkAll(t,
		[2]flow.Element{sStart, inner}, [2]flow.Element{inner, sEnd})

	p, err := process.New("two-hosts")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	split, err := gateways.NewParallelGateway()
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, sub, end} {
		require.NoError(t, p.Add(e))
	}

	// BOTH of the split's outgoing flows target the one Sub-Process, so the
	// two tokens derive the same child path and collide there.
	linkAll(t,
		[2]flow.Element{start, split},
		[2]flow.Element{split, sub},
		[2]flow.Element{split, sub},
		[2]flow.Element{sub, end})

	inst := runIteration(t, p)

	require.Equal(t, Completed, inst.State())
	require.EqualValues(t, 2, body.Load(),
		"both hosts entered the shared scope — the second waited for the "+
			"first rather than being refused or lost")
}

// TestCompositeHostReportsHostingScope (SRD-090.A M3f): while a plain
// Sub-Process's body runs, its host token reports HOSTING A SCOPE — not
// EXECUTING, which is what it said before.
//
// The correction is the same one ADR-025 §2.13 named one level down and fixed
// only inside the runtime: "parked for a child's drain was, from outside the
// runner's own stack, indistinguishable from executing". The executor learned
// the difference (awaitScope); the token kept reporting the old answer.
//
// It is asserted WHILE the body runs, which is the only moment the states
// differ — an inner task holds the scope open until the assertion has been
// made, so this is a fence rather than a sleep.
func TestCompositeHostReportsHostingScope(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	entered := make(chan struct{})
	release := make(chan struct{})

	op, err := gooper.New("hold",
		func(ctx context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			close(entered)

			select {
			case <-release:
			case <-ctx.Done():
			}

			return nil, nil
		})
	require.NoError(t, err)

	inner, err := activities.NewServiceTask("inner", op,
		activities.WithoutParams())
	require.NoError(t, err)

	sp, err := activities.NewSubProcess("held")
	require.NoError(t, err)

	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{sStart, inner, sEnd} {
		require.NoError(t, sp.Add(e))
	}

	linkAll(t,
		[2]flow.Element{sStart, inner}, [2]flow.Element{inner, sEnd})

	var ran atomic.Int32

	after := hitTask(t, "after", &ran, "", 0)

	s, err := snapshot.New(wrapSP(t, "hosting-state", sp, after))
	require.NoError(t, err)

	ep := &capturingProducer{procs: map[string]eventproc.EventProcessor{}}

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, inst.Run(ctx))

	<-entered // the body is inside the scope now

	// The host is the track standing on the Sub-Process node AND hosting
	// its scope. Both halves belong in the predicate: the track reaches the
	// node a moment before it flips to TrackHostingScope, so asserting the
	// state right after finding the track fails whenever the poll lands in
	// that window — which is a flake, not a defect.
	require.Eventually(t, func() bool {
		for _, tr := range inst.tracks {
			st := tr.currentStep()
			if st != nil && st.node != nil && st.node.ID() == sp.ID() &&
				tr.inState(TrackHostingScope) {
				return true
			}
		}

		return false
	}, 2*time.Second, 5*time.Millisecond,
		"the Sub-Process host track, forked into its child scope and waiting "+
			"for it to drain — not executing, and not waiting for an event")

	close(release)

	require.Eventually(t, func() bool { return inst.State() == Completed },
		3*time.Second, 5*time.Millisecond)

	require.EqualValues(t, 1, ran.Load(), "the host resumed onto its outgoing")
}

// TestQueuedScopeOpenSkipsADeadHost (SRD-090.A M4b): a host that queued for a
// composite's scope and then died must not have that scope opened on its
// behalf when the path frees.
//
// A queued host waits inside its roundtrip, which honors its context — so a
// boundary fire or an instance terminate can take it away mid-wait, leaving a
// request in the queue that nobody is listening for. Serving it opens the
// scope and SEEDS THE BODY, which then runs detached from any live token:
// real work and real side effects with no one to receive the result. The
// failure is silent — the body simply runs.
//
// White-box because the ordering is the whole point: the host must be dead
// BEFORE the path frees, which a real race cannot pin.
func TestQueuedScopeOpenSkipsADeadHost(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	inst, ls, host, node := scopeQueueFixture(t)

	// host holds the scope.
	_, answered := openScopeFor(t.Context(), t, ls, host, node)
	require.True(t, answered)
	require.Len(t, ls.scopes, 1)

	// a second host queues behind it.
	dead, err := newTrack(node, inst, nil)
	require.NoError(t, err)

	_, answered = openScopeFor(t.Context(), t, ls, dead, node)
	require.False(t, answered, "queued — its reply is deferred")

	var path scope.DataPath

	var entry *scopeEntry

	for p, e := range ls.scopes {
		path, entry = p, e
	}

	require.Len(t, entry.queue, 1)

	// the queued host is taken away while it waits.
	dead.updateState(TrackCanceled)

	// the path frees.
	entry.active = 0
	ls.completeScope(t.Context(), path, entry)

	require.Empty(t, ls.scopes,
		"the dead host's scope is NOT opened on its behalf — a body seeded "+
			"here would run detached from any live token")
}

// TestQueuedScopeOpenServesTheFirstLiveHost: a dead host in the queue is
// skipped, not treated as a barrier — the live host behind it still gets the
// path.
func TestQueuedScopeOpenServesTheFirstLiveHost(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	inst, ls, host, node := scopeQueueFixture(t)

	_, _ = openScopeFor(t.Context(), t, ls, host, node)

	dead, err := newTrack(node, inst, nil)
	require.NoError(t, err)
	live, err := newTrack(node, inst, nil)
	require.NoError(t, err)

	_, _ = openScopeFor(t.Context(), t, ls, dead, node)
	_, _ = openScopeFor(t.Context(), t, ls, live, node)

	var path scope.DataPath

	var entry *scopeEntry

	for p, e := range ls.scopes {
		path, entry = p, e
	}

	require.Len(t, entry.queue, 2)

	dead.updateState(TrackCanceled)

	entry.active = 0
	ls.completeScope(t.Context(), path, entry)

	fresh, ok := ls.scopes[path]
	require.True(t, ok, "the live host behind the dead one gets the path")
	require.Same(t, live, fresh.host)
}
