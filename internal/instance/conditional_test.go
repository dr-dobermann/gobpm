package instance

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// SRD-048 M3 — loop-local conditional subscriptions, catch flavor. The tests
// drive the loopState machinery directly (the SRD-027 inbound-test style): the
// subject track is built parked by newTrack, the registry armed/swept on the
// test goroutine, and deliveries observed on the track's buffered evtCh.

// condExpr builds a bool GExpression whose value the test controls through
// val, counting evaluations in evals. Extra goexpr options (e.g.
// WithDependencies) append.
func condExpr(
	t *testing.T,
	val *bool,
	evals *int,
	opts ...goexpr.GExpOption,
) data.FormalExpression {
	t.Helper()

	oo := make([]options.Option, 0, len(opts))
	for _, o := range opts {
		oo = append(oo, o)
	}

	ge, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			*evals++

			return values.NewVariable(*val), nil
		}, oo...)
	require.NoError(t, err)

	return ge
}

// condInstance builds an instance whose only wait node is an intermediate
// catch with the given conditional definitions, returning the instance, its
// parked track (built by newTrack — checkNodeType records condDefs and skips
// hub registration), and a loopState seeded the way spawn would (position +
// waiting), WITHOUT arming — each test drives arming itself.
func condInstance(
	t *testing.T,
	def *events.ConditionalEventDefinition,
) (*Instance, *track, *loopState) {
	t.Helper()

	_ = data.CreateDefaultStates()

	p, err := process.New("srd048-cond")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("cond-catch", def)
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, catch, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, catch)
	link(t, catch, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)
	require.True(t, s.HasConditionals,
		"a process with a conditional catch must precompute HasConditionals")

	ep := mockeventproc.NewMockEventProducer(t)
	// A pure-conditional catch registers NOTHING with the hub (SRD-048 FR-7):
	// no RegisterEvent expectation — mockery fails the test on an unexpected
	// call, which IS the assertion.

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)
	inst.tracks = map[string]*track{}
	// assert on the instance's own snapshot: New clones it, and a Clone that
	// drops the flag silently disables the commit signal (caught 2026-07-15).
	require.True(t, inst.s.HasConditionals)

	node := findNode(t, s, "cond-catch")

	tr, err := newTrack(node, inst, nil)
	require.NoError(t, err)
	require.True(t, tr.inState(TrackWaitForEvent))
	require.Len(t, tr.condDefs, 1,
		"checkNodeType must record the conditional definition")

	ls := newLoopState(inst)
	ls.position[tr.ID()] = node
	ls.waiting[tr.ID()] = struct{}{}

	return inst, tr, ls
}

// findNode resolves a node from the snapshot clone set by name — newTrack must
// start on the CLONED node, not the original.
func findNode(t *testing.T, s *snapshot.Snapshot, name string) flow.Node {
	t.Helper()

	for _, n := range s.Nodes {
		if n.Name() == name {
			return n
		}
	}

	t.Fatalf("node %q not found in snapshot", name)

	return nil
}

// mustCondDef wraps NewConditionalEventDefinition for fixtures.
func mustCondDef(
	t *testing.T,
	expr data.FormalExpression,
) *events.ConditionalEventDefinition {
	t.Helper()

	ced, err := events.NewConditionalEventDefinition(expr)
	require.NoError(t, err)

	return ced
}

// TestCondDue — the uniform re-evaluation rule (SRD-048 FR-11/FR-12): no
// statement → always due; a declared statement → due only on overlap.
func TestCondDue(t *testing.T) {
	changes := []data.Change{
		{Path: "order.total", Type: data.ValueUpdated},
		{Path: "customer", Type: data.ValueAdded},
	}

	tests := []struct {
		name string
		deps []string
		due  bool
	}{
		{"no statement", nil, true},
		{"exact", []string{"customer"}, true},
		{"dep is prefix of change", []string{"order"}, true},
		{"change is prefix of dep", []string{"order.total.currency"}, true},
		{"disjoint", []string{"invoice"}, false},
		{"sibling name", []string{"orders"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.due, condDue(tt.deps, changes))
		})
	}
}

// TestConditionalArmTimeFire — a condition already true at arm fires
// immediately (SRD-048 FR-9): the def lands on the parked track's evtCh and
// the registry is torn down by the delivery flip.
func TestConditionalArmTimeFire(t *testing.T) {
	val, evals := true, 0
	def := mustCondDef(t, condExpr(t, &val, &evals))

	_, tr, ls := condInstance(t, def)

	ls.armConditionals(t.Context(), tr)

	require.Equal(t, 1, evals)
	require.Empty(t, ls.conds, "delivery must tear the registry down")
	require.Empty(t, ls.waiting, "delivery must flip the track out of waiting")

	select {
	case got := <-tr.evtCh:
		require.Equal(t, def.ID(), got.ID())
	default:
		t.Fatal("no delivery on evtCh after an arm-time-true arm")
	}
}

// TestConditionalEdgeRule — the false→true edge (Table 10.84, SRD-048 FR-11):
// arm at false; a false re-evaluation keeps it armed; the true one fires.
func TestConditionalEdgeRule(t *testing.T) {
	val, evals := false, 0
	def := mustCondDef(t, condExpr(t, &val, &evals))

	_, tr, ls := condInstance(t, def)
	ctx := t.Context()

	ls.armConditionals(ctx, tr)
	require.Equal(t, 1, evals)
	require.Len(t, ls.conds, 1)
	require.False(t, ls.conds[0].last)

	changes := []data.Change{{Path: "x", Type: data.ValueUpdated}}

	// still false — no fire, still armed.
	ls.sweepConditionals(ctx, changes)
	require.Equal(t, 2, evals)
	require.Len(t, ls.conds, 1)
	require.Empty(t, tr.evtCh)

	// flips true — the edge fires and tears down.
	val = true
	ls.sweepConditionals(ctx, changes)
	require.Equal(t, 3, evals)
	require.Empty(t, ls.conds)

	select {
	case got := <-tr.evtCh:
		require.Equal(t, def.ID(), got.ID())
	default:
		t.Fatal("no delivery after the false→true edge")
	}
}

// TestConditionalDependencyFiltering — a declared statement narrows
// re-evaluation to overlapping commits; an undeclared expression re-evaluates
// on every commit (SRD-048 FR-11/FR-12).
func TestConditionalDependencyFiltering(t *testing.T) {
	val, declEvals := false, 0
	declared := mustCondDef(t, condExpr(t, &val, &declEvals,
		goexpr.WithDependencies("order")))

	_, tr, ls := condInstance(t, declared)
	ctx := t.Context()

	ls.armConditionals(ctx, tr)
	require.Equal(t, 1, declEvals, "arm-time evaluation is unconditional")

	// disjoint commit — skipped.
	ls.sweepConditionals(ctx,
		[]data.Change{{Path: "customer", Type: data.ValueAdded}})
	require.Equal(t, 1, declEvals)

	// overlapping commit (segment-prefix) — evaluated.
	ls.sweepConditionals(ctx,
		[]data.Change{{Path: "order.total", Type: data.ValueUpdated}})
	require.Equal(t, 2, declEvals)
}

// TestConditionalMultiFireVoiding — two conditional subscriptions of one
// parked track (the event-based-gateway multi-arm shape: the gateway's
// Definitions() unions its arms') turn true in one sweep: the first (in
// arming order) fires and its delivery flips the track, tearing BOTH entries
// down; the second fire is void (ADR-006 v.3 §2.7 multi-fire ordering).
// Exactly one delivery lands. The second watch is seeded directly — the
// sweep contract does not care how an entry was armed; the end-to-end
// multi-arm arming rides the M4 EBG tests.
func TestConditionalMultiFireVoiding(t *testing.T) {
	val, evals1, evals2 := false, 0, 0
	def1 := mustCondDef(t, condExpr(t, &val, &evals1))
	def2 := mustCondDef(t, condExpr(t, &val, &evals2))

	_, tr, ls := condInstance(t, def1)
	ctx := t.Context()

	ls.armConditionals(ctx, tr)
	require.Len(t, ls.conds, 1)

	ls.conds = append(ls.conds, &condWatch{
		track: tr,
		node:  ls.conds[0].node,
		def:   def2,
	})

	val = true
	ls.sweepConditionals(ctx,
		[]data.Change{{Path: "x", Type: data.ValueUpdated}})

	require.Empty(t, ls.conds)

	got := <-tr.evtCh
	require.Equal(t, def1.ID(), got.ID(), "arming order wins")

	select {
	case extra := <-tr.evtCh:
		t.Fatalf("voided fire delivered anyway: %s", extra.ID())
	default:
	}
}

// TestConditionalEvalFailureFailsInstance — an erroring condition fails the
// instance (fail + stopAll): a wait the engine cannot evaluate is meaningless
// (SRD-048 FR-13).
func TestConditionalEvalFailureFailsInstance(t *testing.T) {
	_ = data.CreateDefaultStates()

	bad, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return nil, fmt.Errorf("boom")
		})
	require.NoError(t, err)

	def := mustCondDef(t, bad)

	_, tr, ls := condInstance(t, def)

	ls.armConditionals(t.Context(), tr)

	require.True(t, ls.stopping,
		"an evaluation failure must stop the instance")
	require.Error(t, ls.inst.LastErr())
}

// TestSignalDataCommitGates — the track-side emit gate (SRD-048 FR-10/NFR-1):
// no changes, no HasConditionals, or a non-Active instance → no emit (the
// call returns instead of blocking on the un-drained loop channel).
func TestSignalDataCommitGates(t *testing.T) {
	inst, tr, _ := condInstance(t,
		mustCondDef(t, condExpr(t, new(bool), new(int))))

	changes := []data.Change{{Path: "x", Type: data.ValueUpdated}}

	// instance is Created (not Active): must return without emitting even
	// though HasConditionals is true and changes are non-empty.
	tr.signalDataCommit(tr.currentStep().node, changes)

	// empty changes: gated regardless of state.
	tr.signalDataCommit(tr.currentStep().node, nil)

	// HasConditionals=false: gated regardless of changes.
	inst.s.HasConditionals = false
	tr.signalDataCommit(tr.currentStep().node, changes)
}

// TestSnapshotHasConditionalsFalse — a conditional-free process precomputes
// false (NFR-1), so its tracks never emit evDataCommit.
func TestSnapshotHasConditionalsFalse(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("srd048-plain")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add(end))
	link(t, start, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)
	require.False(t, s.HasConditionals)
}

// TestEvDataCommitKindName — the kind-name table row (the trackEventKindNames
// sync convention).
func TestEvDataCommitKindName(t *testing.T) {
	require.Equal(t, "dataCommit", evDataCommit.String())
}


// TestConditionalCoverageEdges — the small defensive branches of the SRD-048
// machinery: a non-DependencyLister expression declares nothing; a non-bool
// evaluation result is a classified error; a stopping/empty sweep and a
// repeated teardown are no-ops; nodeNameOf guards nil.
func TestConditionalCoverageEdges(t *testing.T) {
	_ = data.CreateDefaultStates()

	t.Run("condDeps without the capability", func(t *testing.T) {
		me := mockdata.NewMockFormalExpression(t)
		me.EXPECT().ResultType().Return("bool").Once()

		def := mustCondDef(t, me)
		require.Nil(t, condDeps(def))
	})

	t.Run("non-bool evaluation result is an error", func(t *testing.T) {
		me := mockdata.NewMockFormalExpression(t)
		me.EXPECT().ResultType().Return("bool")

		def := mustCondDef(t, me)

		_, tr, ls := condInstance(t, mustCondDef(t,
			condExpr(t, new(bool), new(int))))
		_ = tr

		me.EXPECT().Language().Return("mock").Maybe()
		me.EXPECT().Evaluate(mock.Anything, mock.Anything).
			Return(values.NewVariable(42), nil)

		_, err := ls.evalCondition(t.Context(), def, ls.inst.sc.root)
		require.Error(t, err)
		require.Contains(t, err.Error(), "non-boolean")
	})

	t.Run("stopping and empty sweeps are no-ops", func(t *testing.T) {
		_, tr, ls := condInstance(t, mustCondDef(t,
			condExpr(t, new(bool), new(int))))

		changes := []data.Change{{Path: "x", Type: data.ValueUpdated}}

		// empty registry — nothing armed yet.
		ls.sweepConditionals(t.Context(), changes)

		ls.armConditionals(t.Context(), tr)
		ls.stopping = true
		ls.sweepConditionals(t.Context(), changes)
		require.Len(t, ls.conds, 1, "a stopping sweep must not evaluate")

		// repeated teardown — the second call hits the empty early return.
		ls.stopping = false
		ls.clearConds(tr.ID())
		ls.clearConds(tr.ID())
		require.Empty(t, ls.conds)
	})

	t.Run("nodeNameOf guards nil", func(t *testing.T) {
		require.Equal(t, noneLabel, nodeNameOf(nil))
	})
}

// TestSignalDataCommitEmits — the positive emit path (SRD-048 FR-10): an
// Active instance with conditionals emits evDataCommit carrying the diff.
func TestSignalDataCommitEmits(t *testing.T) {
	inst, tr, _ := condInstance(t,
		mustCondDef(t, condExpr(t, new(bool), new(int))))

	inst.setState(Active)

	changes := []data.Change{{Path: "order", Type: data.ValueUpdated}}

	got := make(chan trackEvent, 1)
	go func() { got <- <-inst.events }()

	tr.signalDataCommit(tr.currentStep().node, changes)

	ev := <-got
	require.Equal(t, evDataCommit, ev.kind)
	require.Equal(t, changes, ev.changes)
	require.Equal(t, tr, ev.track)
}

// TestSnapshotHasConditionalsSkipsNonEvents — the precompute pass skips
// non-event nodes (a gateway) and event nodes without conditional defs.
func TestSnapshotHasConditionalsSkipsNonEvents(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("srd048-gw")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	gw, err := gateways.NewParallelGateway()
	require.NoError(t, err)

	// a non-conditional event definition exercises the def-scan skip.
	arm, armEnd, _ := ebSignalArm(t, "not-conditional")

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, gw, arm, armEnd, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, gw)
	link(t, gw, end)
	link(t, gw, arm)
	link(t, arm, armEnd)

	s, err := snapshot.New(p)
	require.NoError(t, err)
	require.False(t, s.HasConditionals)
}

// TestConditionalSweepEvalFailure — an evaluation failure DURING a sweep (not
// at arm) also fails the instance (SRD-048 FR-13).
func TestConditionalSweepEvalFailure(t *testing.T) {
	_ = data.CreateDefaultStates()

	fail := false
	expr, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			if fail {
				return nil, fmt.Errorf("boom")
			}

			return values.NewVariable(false), nil
		})
	require.NoError(t, err)

	_, tr, ls := condInstance(t, mustCondDef(t, expr))
	ctx := t.Context()

	ls.armConditionals(ctx, tr)
	require.False(t, ls.stopping)

	fail = true
	ls.sweepConditionals(ctx,
		[]data.Change{{Path: "x", Type: data.ValueUpdated}})

	require.True(t, ls.stopping)
	require.Error(t, ls.inst.LastErr())
}

// TestClearCondsKeepsOtherTracks — teardown is per-track: clearing one track's
// entries keeps another's armed (SRD-048 FR-8).
func TestClearCondsKeepsOtherTracks(t *testing.T) {
	_, tr, ls := condInstance(t, mustCondDef(t,
		condExpr(t, new(bool), new(int))))

	ls.armConditionals(t.Context(), tr)
	require.Len(t, ls.conds, 1)

	other := &track{}
	ls.conds = append(ls.conds, &condWatch{track: other})

	ls.clearConds(tr.ID())

	require.Len(t, ls.conds, 1)
	require.Same(t, other, ls.conds[0].track)
}

// condBoundaryHarness builds the guarded-host instance with an extra
// Conditional boundary (SRD-048 M4): the base interrupting signal boundary
// stays hub-registered (the recordingProducer sees it), the conditional one
// is loop-owned. Returns the seeded loopState and the bare host track (with
// a cancellable ctx, so an interrupting fire is observable).
func condBoundaryHarness(
	t *testing.T,
	val *bool,
	evals *int,
	interrupting bool,
) (*Instance, *track, *loopState, context.CancelFunc) {
	t.Helper()

	ep := &recordingProducer{}

	inst, host, _, _, _ := guardedHostInstance(t, ep,
		func(h flow.ActivityNode, p *process.Process) {
			ced := mustCondDef(t, condExpr(t, val, evals))

			be, err := events.NewBoundaryEvent("bndCond", h, ced, interrupting)
			require.NoError(t, err)

			exc, err := events.NewEndEvent("excCond")
			require.NoError(t, err)

			require.NoError(t, p.Add(be))
			require.NoError(t, p.Add(exc))
			_, err = flow.Link(be, exc)
			require.NoError(t, err)
		})
	inst.tracks = map[string]*track{}
	require.True(t, inst.s.HasConditionals,
		"a conditional boundary must flip the snapshot flag")

	tr := bareTrack(t, inst, host)

	ctx, cancel := context.WithCancel(t.Context())
	tr.ctx = ctx
	tr.cancel = cancel

	return inst, tr, newLoopState(inst), cancel
}

// TestConditionalBoundaryArmTimeInterrupting — an interrupting Conditional
// boundary whose condition is already true at arm fires during arming: the
// exception flow forks, the host is cancelled, the watches and the cond
// entry are torn down (SRD-048 FR-9/FR-15).
func TestConditionalBoundaryArmTimeInterrupting(t *testing.T) {
	val, evals := true, 0
	inst, tr, ls, cancel := condBoundaryHarness(t, &val, &evals, true)
	defer cancel()

	before := trackIDSet(inst)

	ls.armBoundaries(t.Context(), tr, tr.currentStep().node)

	require.Equal(t, 1, evals)
	require.Error(t, tr.ctx.Err(), "the host is cancelled on the arm-time fire")
	require.NotContains(t, ls.watchers, tr.ID())
	require.Empty(t, ls.conds)

	forked := newTrackIDs(before, inst)
	require.Len(t, forked, 1, "the exception flow spawned one track")
	drainUntilEnd(t, inst, forked[0])
}

// TestConditionalBoundaryInterruptingSweep — armed at false, a commit sweep
// flipping the condition true fires the interrupting boundary: exception
// fork + host cancel + teardown (SRD-048 FR-11/FR-15).
func TestConditionalBoundaryInterruptingSweep(t *testing.T) {
	val, evals := false, 0
	inst, tr, ls, cancel := condBoundaryHarness(t, &val, &evals, true)
	defer cancel()

	ls.armBoundaries(t.Context(), tr, tr.currentStep().node)
	require.Len(t, ls.watchers[tr.ID()], 2,
		"the signal watch (hub) and the conditional watch (loop) are armed")
	require.Len(t, ls.conds, 1)
	require.NoError(t, tr.ctx.Err())

	before := trackIDSet(inst)

	val = true
	ls.sweepConditionals(t.Context(),
		[]data.Change{{Path: "x", Type: data.ValueUpdated}})

	require.Error(t, tr.ctx.Err(), "the host is cancelled on the sweep fire")
	require.NotContains(t, ls.watchers, tr.ID())
	require.Empty(t, ls.conds, "disarm cleared the conditional entry")

	forked := newTrackIDs(before, inst)
	require.Len(t, forked, 1)
	drainUntilEnd(t, inst, forked[0])
}

// TestConditionalBoundaryNonInterrupting — a non-interrupting fire forks a
// parallel track, leaves the host running and the entry armed with
// last=true; a re-fire needs a fresh false→true edge (Table 10.84,
// SRD-048 FR-15).
func TestConditionalBoundaryNonInterrupting(t *testing.T) {
	val, evals := false, 0
	inst, tr, ls, cancel := condBoundaryHarness(t, &val, &evals, false)
	defer cancel()

	ctx := t.Context()
	changes := []data.Change{{Path: "x", Type: data.ValueUpdated}}

	ls.armBoundaries(ctx, tr, tr.currentStep().node)
	require.Len(t, ls.conds, 1)

	// first edge — fires, host runs on, entry stays armed.
	before := trackIDSet(inst)
	val = true
	ls.sweepConditionals(ctx, changes)

	require.NoError(t, tr.ctx.Err(), "a non-interrupting fire keeps the host")
	require.Contains(t, ls.watchers, tr.ID(), "the watch list stays armed")
	require.Len(t, ls.conds, 1, "the entry stays armed")
	require.True(t, ls.conds[0].last)

	forked := newTrackIDs(before, inst)
	require.Len(t, forked, 1, "the parallel flow spawned one track")
	drainUntilEnd(t, inst, forked[0])

	// staying true — no edge, no second fork.
	before = trackIDSet(inst)
	ls.sweepConditionals(ctx, changes)
	require.Empty(t, newTrackIDs(before, inst))

	// false re-arms; the next true is a fresh edge and fires again.
	val = false
	ls.sweepConditionals(ctx, changes)
	require.False(t, ls.conds[0].last)

	before = trackIDSet(inst)
	val = true
	ls.sweepConditionals(ctx, changes)

	forked = newTrackIDs(before, inst)
	require.Len(t, forked, 1, "a fresh false→true edge re-fires")
	drainUntilEnd(t, inst, forked[0])
}

// ebCondGateInstance builds start → event-based gate → {conditional arm,
// signal arm} and parks a track at the gate: the gateway's Definitions()
// union puts the conditional def in t.condDefs (loop-armed) and the signal
// def on the hub path (SRD-048 FR-14).
func ebCondGateInstance(
	t *testing.T,
	val *bool,
	evals *int,
) (*Instance, *track, *loopState, flow.EventDefinition, flow.EventDefinition) {
	t.Helper()

	_ = data.CreateDefaultStates()

	p, err := process.New("srd048-ebg")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	gate, err := gateways.NewEventBasedGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	ced := mustCondDef(t, condExpr(t, val, evals))
	condArm, err := events.NewIntermediateCatchEvent("arm-cond", ced)
	require.NoError(t, err)
	condEnd, err := events.NewEndEvent("end-cond")
	require.NoError(t, err)

	sigArm, sigEnd, sigDef := ebSignalArm(t, "race")

	for _, e := range []flow.Element{start, gate, condArm, condEnd, sigArm, sigEnd} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, gate)
	link(t, gate, condArm)
	link(t, condArm, condEnd)
	link(t, gate, sigArm)
	link(t, sigArm, sigEnd)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)
	ep.EXPECT().RegisterEvent(mock.Anything, mock.Anything).Return(nil).Maybe()

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)
	inst.tracks = map[string]*track{}

	gateNode := s.Nodes[gate.ID()]
	require.NotNil(t, gateNode)

	tr, err := newTrack(gateNode, inst, nil)
	require.NoError(t, err)
	require.True(t, tr.inState(TrackWaitForEvent))
	require.Len(t, tr.condDefs, 1,
		"the gateway's Definitions() union surfaces the conditional arm")

	ls := newLoopState(inst)
	ls.position[tr.ID()] = gateNode
	ls.waiting[tr.ID()] = struct{}{}

	return inst, tr, ls, ced, sigDef
}

// TestEBGConditionalArmWins — the conditional arm's fire wins the deferred
// choice: the def is delivered and the flip drops any later arm delivery
// (SRD-048 FR-14).
func TestEBGConditionalArmWins(t *testing.T) {
	val, evals := false, 0
	_, tr, ls, ced, sigDef := ebCondGateInstance(t, &val, &evals)
	ctx := t.Context()

	ls.armConditionals(ctx, tr)

	val = true
	ls.sweepConditionals(ctx,
		[]data.Change{{Path: "x", Type: data.ValueUpdated}})

	got := <-tr.evtCh
	require.Equal(t, ced.ID(), got.ID())

	// the losing signal arm's later delivery is dropped by the flip.
	ls.dispatchToParked(ctx, trackEvent{kind: evDeliver, track: tr, eDef: sigDef})
	require.Empty(t, tr.evtCh)
}

// TestEBGConditionalArmLoses — the signal arm delivers first: the flip tears
// the conditional entry down, and a later condition flip fires nothing
// (SRD-048 FR-14).
func TestEBGConditionalArmLoses(t *testing.T) {
	val, evals := false, 0
	_, tr, ls, _, sigDef := ebCondGateInstance(t, &val, &evals)
	ctx := t.Context()

	ls.armConditionals(ctx, tr)
	require.Len(t, ls.conds, 1)

	ls.dispatchToParked(ctx, trackEvent{kind: evDeliver, track: tr, eDef: sigDef})

	got := <-tr.evtCh
	require.Equal(t, sigDef.ID(), got.ID())
	require.Empty(t, ls.conds, "the flip tore the conditional entry down")

	val = true
	ls.sweepConditionals(ctx,
		[]data.Change{{Path: "x", Type: data.ValueUpdated}})
	require.Empty(t, tr.evtCh, "the losing conditional fires nothing")
}

// TestConditionalBoundaryArmEvalFailure — an erroring condition at boundary
// arm-time fails the instance and aborts arming (SRD-048 FR-13/FR-15).
func TestConditionalBoundaryArmEvalFailure(t *testing.T) {
	ep := &recordingProducer{}

	inst, host, _, _, _ := guardedHostInstance(t, ep,
		func(h flow.ActivityNode, p *process.Process) {
			bad := goexpr.Must(nil,
				data.MustItemDefinition(values.NewVariable(false)),
				func(_ context.Context, _ data.Source) (data.Value, error) {
					return nil, fmt.Errorf("boom")
				})

			be, err := events.NewBoundaryEvent("bndBad", h,
				mustCondDef(t, bad), true)
			require.NoError(t, err)

			exc, err := events.NewEndEvent("excBad")
			require.NoError(t, err)

			require.NoError(t, p.Add(be))
			require.NoError(t, p.Add(exc))
			_, err = flow.Link(be, exc)
			require.NoError(t, err)
		})
	inst.tracks = map[string]*track{}

	tr := bareTrack(t, inst, host)
	ls := newLoopState(inst)

	ls.armBoundaries(t.Context(), tr, host)

	require.True(t, ls.stopping)
	require.Error(t, inst.LastErr())
}

// TestConditionalSurvivesMove — the lost-wake-up regression
// (TestConditionalEventsE2E flake): a track that walks onto a conditional
// catch emits evWaiting (arming the watch) and THEN evMoved — whose
// boundary disarm must NOT tear the fresh catch subscription down. The
// deterministic pin of the map-order-selected interleaving.
func TestConditionalSurvivesMove(t *testing.T) {
	val, evals := false, 0
	def := mustCondDef(t, condExpr(t, &val, &evals))

	_, tr, ls := condInstance(t, def)
	ctx := t.Context()

	catchNode := ls.position[tr.ID()]

	// the failing interleaving: arm (evWaiting) then the same track's
	// evMoved-driven boundary disarm.
	ls.armConditionalsAt(ctx, tr, catchNode)
	require.Len(t, ls.conds, 1)

	ls.disarmBoundaries(tr.ID())
	require.Len(t, ls.conds, 1,
		"the boundary disarm must not kill a catch subscription")

	// the commit now finds the armed watch and fires it.
	val = true
	ls.sweepConditionals(ctx,
		[]data.Change{{Path: "x", Type: data.ValueUpdated}})

	got := <-tr.evtCh
	require.Equal(t, def.ID(), got.ID())
}
