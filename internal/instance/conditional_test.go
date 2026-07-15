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

		_, err := ls.evalCondition(t.Context(), def)
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
		ls.clearConds(tr)
		ls.clearConds(tr)
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

	ls.clearConds(tr)

	require.Len(t, ls.conds, 1)
	require.Same(t, other, ls.conds[0].track)
}
