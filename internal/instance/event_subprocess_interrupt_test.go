package instance

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/stretchr/testify/require"
)

// scopedSP builds a sub-process body (s-start → stuck-receive → s-end) that
// blocks on a never-delivered message, plus the given event-sub handler — so an
// interrupting handler has a live sibling track to cancel.
func scopedSP(
	t *testing.T, name string, handler *activities.SubProcess,
) *activities.SubProcess {
	t.Helper()

	sp, err := activities.NewSubProcess(name)
	require.NoError(t, err)

	sStart, err := events.NewStartEvent(name + "-start")
	require.NoError(t, err)
	stuck := blockedReceive(t, name+"-stuck")
	sEnd, err := events.NewEndEvent(name + "-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{sStart, stuck, sEnd, handler} {
		require.NoError(t, sp.Add(e))
	}
	linkAll(t, [2]flow.Element{sStart, stuck}, [2]flow.Element{stuck, sEnd})

	return sp
}

// TestInterruptingSignalCancelsScope (SRD-052 FR-7/FR-9): a Signal-triggered
// interrupting handler fires, cancels its scope's blocked sibling, runs its own
// flow, and — reaching its End without re-throwing — absorbs the event so the
// host resumes on its NORMAL outgoing.
func TestInterruptingSignalCancelsScope(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var ran atomic.Int32
	es := sigEventSub(t, "es", &ran)

	sp := scopedSP(t, "guarded", es)

	var after atomic.Int32
	p := wrapSP(t, "sig-cancel", sp, hitTask(t, "after", &after, "", 0))

	s, err := snapshot.New(p)
	require.NoError(t, err)

	cp := &capturingProducer{procs: map[string]eventproc.EventProcessor{}}
	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), cp, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	// wait until the handler armed (its scope opened), then fire its signal.
	var w *scopeHandlerWatch
	require.Eventually(t, func() bool {
		w = cp.scopeWatch()

		return w != nil
	}, 3*time.Second, 5*time.Millisecond)

	require.NoError(t, w.ProcessEvent(ctx, w.def))

	require.Eventually(t, func() bool { return inst.State() == Completed },
		3*time.Second, 5*time.Millisecond)
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, ran.Load(), "the handler ran")
	require.EqualValues(t, 1, after.Load(),
		"the host absorbed the event and resumed on its normal flow")
}

// TestInterruptingHandlerCancelsNestedScope (SRD-052 FR-7): an interrupting
// handler cancels a scope that itself contains an OPEN nested sub-process — the
// nested scope and its blocked track die as a unit, the handler's own scope is
// spared, and the host resumes.
func TestInterruptingHandlerCancelsNestedScope(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// nested sub-process: i-start → inner-stuck (blocks) → i-end.
	inner, err := activities.NewSubProcess("inner")
	require.NoError(t, err)
	iStart, err := events.NewStartEvent("i-start")
	require.NoError(t, err)
	iEnd, err := events.NewEndEvent("i-end")
	require.NoError(t, err)
	innerStuck := blockedReceive(t, "inner-stuck")
	for _, e := range []flow.Element{iStart, innerStuck, iEnd} {
		require.NoError(t, inner.Add(e))
	}
	linkAll(t, [2]flow.Element{iStart, innerStuck},
		[2]flow.Element{innerStuck, iEnd})

	// outer scope: o-start → inner → o-end, PLUS the interrupting handler.
	var ran atomic.Int32
	outer, err := activities.NewSubProcess("outer")
	require.NoError(t, err)
	oStart, err := events.NewStartEvent("o-start")
	require.NoError(t, err)
	oEnd, err := events.NewEndEvent("o-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{oStart, inner, oEnd, sigEventSub(t, "es2", &ran)} {
		require.NoError(t, outer.Add(e))
	}
	linkAll(t, [2]flow.Element{oStart, inner}, [2]flow.Element{inner, oEnd})

	var after atomic.Int32
	p := wrapSP(t, "nested-cancel", outer, hitTask(t, "after", &after, "", 0))

	s, err := snapshot.New(p)
	require.NoError(t, err)

	cp := &capturingProducer{procs: map[string]eventproc.EventProcessor{}}
	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), cp, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	// wait until BOTH the handler armed (outer scope open) and the inner scope's
	// blocked receive registered — so a live nested scope exists to cancel.
	var w *scopeHandlerWatch
	require.Eventually(t, func() bool {
		w = cp.scopeWatch()

		return w != nil && cp.numProcs() >= 2
	}, 3*time.Second, 5*time.Millisecond)

	require.NoError(t, w.ProcessEvent(ctx, w.def))

	require.Eventually(t, func() bool { return inst.State() == Completed },
		3*time.Second, 5*time.Millisecond)
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, ran.Load(), "the handler ran")
	require.EqualValues(t, 1, after.Load(), "the host resumed after the nested cancel")
}

// TestConditionalStartHandlerCancelsScope (SRD-052 FR-7/FR-9): a Conditional
// start handler whose condition is true fires at arm time (when the scope
// opens), cancelling the scope's blocked sibling and running to completion —
// the deferred conditional-start driving a full cancel-and-run, no external
// trigger.
func TestConditionalStartHandlerCancelsScope(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var ran atomic.Int32
	es := condEventSub(t, "ces", &ran)

	sp := scopedSP(t, "cbody", es)

	var after atomic.Int32
	inst := runInstance(t,
		wrapSP(t, "cond-cancel", sp, hitTask(t, "after", &after, "", 0)))

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, ran.Load(), "the conditional handler ran")
	require.EqualValues(t, 1, after.Load(), "the host resumed")
}

// errEventSubRan builds an Error-triggered Event Sub-Process whose task bumps
// ran — the observable twin of errEventSub, for the catch-and-run path.
func errEventSubRan(
	t *testing.T, name, code string, ran *atomic.Int32,
) *activities.SubProcess {
	t.Helper()

	es, err := activities.NewSubProcess(name, activities.WithTriggeredByEvent())
	require.NoError(t, err)

	start, err := events.NewStartEvent(name+"-start",
		events.WithErrorTrigger(errDef(t, name+"-err", code)))
	require.NoError(t, err)

	task := hitTask(t, name+"-task", ran, "", 0)
	end, err := events.NewEndEvent(name + "-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, task, end} {
		require.NoError(t, es.Add(e))
	}
	linkAll(t, [2]flow.Element{start, task}, [2]flow.Element{task, end})

	return es
}

// TestErrorEventSubCatchesOnChain (SRD-052 FR-8/FR-9): an Error thrown inside a
// scope is caught by the scope's own Error-triggered Event Sub-Process on the
// §2.6 chain walk (the inline handler is the innermost catcher); the handler
// runs, absorbs the fault, and the host completes normally.
func TestErrorEventSubCatchesOnChain(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var ran atomic.Int32

	sp, err := activities.NewSubProcess("ebody")
	require.NoError(t, err)

	sStart, err := events.NewStartEvent("e-start")
	require.NoError(t, err)
	boom := throwTask(t, "boom", "E9")
	sEnd, err := events.NewEndEvent("e-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{
		sStart, boom, sEnd, errEventSubRan(t, "eh", "E9", &ran),
	} {
		require.NoError(t, sp.Add(e))
	}
	// s-start → boom (fails with BpmnError E9) → the eh handler catches it on
	// the scope chain before it can reach s-end.
	linkAll(t, [2]flow.Element{sStart, boom}, [2]flow.Element{boom, sEnd})

	var after atomic.Int32
	inst := runInstance(t,
		wrapSP(t, "err-catch", sp, hitTask(t, "after", &after, "", 0)))

	require.Equal(t, Completed, inst.State(),
		"the inline Error handler caught and absorbed the fault")
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, ran.Load(), "the error handler ran")
	require.EqualValues(t, 1, after.Load(), "the host resumed on its normal flow")
}

// openInstance builds an unstarted instance (its root scope open) and a fresh
// loop state, for white-box exercise of the loop methods on the test goroutine.
func openInstance(t *testing.T) (*Instance, *loopState) {
	t.Helper()

	p, err := process.New("wb")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	for _, e := range []flow.Element{start, end} {
		require.NoError(t, p.Add(e))
	}
	linkAll(t, [2]flow.Element{start, end})

	s, err := snapshot.New(p)
	require.NoError(t, err)
	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&capturingProducer{procs: map[string]eventproc.EventProcessor{}}, nil)
	require.NoError(t, err)
	inst.tracks = map[string]*track{}

	return inst, newLoopState(inst)
}

// TestRunScopeHandlerErrorPaths (SRD-052 FR-7): the cancel-and-run's defensive
// guards fault the instance rather than proceed — an unopenable enclosing path,
// a payload that cannot bind (its scope is not open), and a handler node that is
// not a NodeExecutor.
func TestRunScopeHandlerErrorPaths(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("unopenable enclosing path faults", func(t *testing.T) {
		inst, ls := openInstance(t)
		es := sigEventSub(t, "e", &atomic.Int32{})
		w := &scopeHandlerWatch{inst: inst, handler: es, path: scope.EmptyDataPath}

		ls.runScopeHandler(t.Context(), w, nil)

		require.True(t, ls.stopping)
		require.Error(t, inst.LastErr())
	})

	t.Run("payload bind failure faults", func(t *testing.T) {
		inst, ls := openInstance(t)
		es := sigEventSub(t, "e", &atomic.Int32{})
		_, def, ok := triggeredStartOf(es) // the signal def carries an item
		require.True(t, ok)

		unopened, err := inst.sc.root.Append("never-opened")
		require.NoError(t, err)
		w := &scopeHandlerWatch{inst: inst, handler: es, path: unopened}

		ls.runScopeHandler(t.Context(), w, def) // bind targets a closed scope

		require.True(t, ls.stopping)
		require.Error(t, inst.LastErr())
	})

	t.Run("non-executor handler faults", func(t *testing.T) {
		inst, ls := openInstance(t)
		bn, err := flow.NewBaseNode("plain")
		require.NoError(t, err)
		w := &scopeHandlerWatch{
			inst: inst, handler: nonExecNode{bn}, path: inst.sc.root,
		}

		ls.runScopeHandler(t.Context(), w, nil)

		require.True(t, ls.stopping)
		require.Error(t, inst.LastErr())
	})
}

// TestSharedInterruptingBudget (SRD-052 FR-6): the first interrupting fire in a
// scope spends its budget; both the event-sub fire path and the boundary fire
// path consult it, so a second interrupting fire in the same scope is
// suppressed.
func TestSharedInterruptingBudget(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("event-sub fire suppressed when already interrupted", func(t *testing.T) {
		es := sigEventSub(t, "b", &atomic.Int32{})
		start, def, ok := triggeredStartOf(es)
		require.True(t, ok)

		rec := &obsRecorder{}
		inst := &Instance{}
		inst.observers = []obsReg{{fn: rec.record, id: 1}}
		ls := newLoopState(inst)
		ls.scopeHandlers[scope.EmptyDataPath] = []*scopeHandlerWatch{{
			inst: inst, handler: es, start: start, def: def,
			path: scope.EmptyDataPath,
		}}
		ls.scopeInterrupted[scope.EmptyDataPath] = true // a prior fire spent it

		ls.fireScopeHandler(t.Context(),
			trackEvent{kind: evScopeHandlerFire, node: es})

		require.False(t, rec.handlerPhases()[observability.PhaseFired],
			"the spent budget suppresses the second event-sub fire")
	})

	t.Run("boundary fire suppressed when already interrupted", func(t *testing.T) {
		sp, err := activities.NewSubProcess("comp")
		require.NoError(t, err)
		cStart, err := events.NewStartEvent("c-start")
		require.NoError(t, err)
		cEnd, err := events.NewEndEvent("c-end")
		require.NoError(t, err)
		require.NoError(t, sp.Add(cStart))
		require.NoError(t, sp.Add(cEnd))
		linkAll(t, [2]flow.Element{cStart, cEnd})

		sig, err := events.NewSignal("bsig",
			data.MustItemDefinition(values.NewVariable(1)))
		require.NoError(t, err)
		sdef, err := events.NewSignalEventDefinition(sig)
		require.NoError(t, err)
		bnd, err := events.NewBoundaryEvent("bnd", sp, sdef, true)
		require.NoError(t, err)

		rec := &obsRecorder{}
		inst := &Instance{}
		inst.observers = []obsReg{{fn: rec.record, id: 1}}
		ls := newLoopState(inst)

		host := &track{
			BaseElement: *foundation.MustBaseElement(),
			scopePath:   scope.DataPath("/p"),
		}
		ls.position[host.ID()] = sp
		ls.watchers[host.ID()] = []*boundaryWatch{
			{host: host, boundary: bnd, def: sdef},
		}
		child, err := host.scopePath.Append(scopeSegment(sp))
		require.NoError(t, err)
		ls.scopeInterrupted[child] = true // an event-sub already interrupted it

		ls.fireBoundary(t.Context(), trackEvent{track: host, node: bnd})

		boundaryFired := false
		for _, e := range rec.events {
			if e.Kind == observability.KindBoundary &&
				e.Phase == observability.PhaseFired {
				boundaryFired = true
			}
		}
		require.False(t, boundaryFired,
			"the spent budget suppresses the competing boundary fire")
	})
}
