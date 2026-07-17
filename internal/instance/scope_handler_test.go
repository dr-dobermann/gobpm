package instance

import (
	"sync/atomic"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/stretchr/testify/require"
)

// runObserved builds and runs an instance of p with a fact recorder attached
// before the run, returning the recorder once the instance is terminal.
func runObserved(t *testing.T, p *process.Process) *obsRecorder {
	t.Helper()

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&capturingProducer{procs: map[string]eventproc.EventProcessor{}}, nil)
	require.NoError(t, err)

	rec := &obsRecorder{}
	inst.AddObserver(rec.record)

	runToDone(t, inst)

	return rec
}

// handlerPhases returns the set of scope-handler lifecycle phases the recorder
// saw — the KindBoundary facts carrying a scope_path detail (which distinguishes
// an Event Sub-Process handler from a plain boundary event).
func (r *obsRecorder) handlerPhases() map[observability.Phase]bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := map[observability.Phase]bool{}
	for _, e := range r.events {
		if e.Kind != observability.KindBoundary {
			continue
		}
		if _, ok := e.Details[observability.AttrScopePath]; ok {
			out[e.Phase] = true
		}
	}

	return out
}

// TestRootHandlerArmedAndDisarmed (SRD-052 FR-5): a top-level Event
// Sub-Process is armed at the instance root and disarmed when the instance
// terminates.
func TestRootHandlerArmedAndDisarmed(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// process: start → task → end, PLUS a top-level event-sub.
	p, err := process.New("root-handler")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	task := hitTask(t, "task", &atomic.Int32{}, "", 0)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	for _, e := range []flow.Element{start, task, end, sigEventSub(t, "root-es", &atomic.Int32{})} {
		require.NoError(t, p.Add(e))
	}
	link(t, start, task)
	link(t, task, end)

	phases := runObserved(t, p).handlerPhases()
	require.True(t, phases[observability.PhaseArmed], "the root handler armed")
	require.True(t, phases[observability.PhaseDisarmed],
		"it disarmed at instance completion")
}

// TestScopeHandlerArmedOnOpenDisarmedOnDrain (SRD-052 FR-5): an Event
// Sub-Process inside an embedded sub-process arms when the scope opens and
// disarms when the scope drains.
func TestScopeHandlerArmedOnOpenDisarmedOnDrain(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// embedded sub: None start → task → end, PLUS an inner event-sub.
	sp, err := activities.NewSubProcess("body")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	sTask := hitTask(t, "body-task", &atomic.Int32{}, "", 0)
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{sStart, sTask, sEnd, sigEventSub(t, "inner-es", &atomic.Int32{})} {
		require.NoError(t, sp.Add(e))
	}
	linkAll(t, [2]flow.Element{sStart, sTask}, [2]flow.Element{sTask, sEnd})

	after := hitTask(t, "after", &atomic.Int32{}, "", 0)
	p := wrapSP(t, "scoped-handler", sp, after)

	phases := runObserved(t, p).handlerPhases()
	require.True(t, phases[observability.PhaseArmed],
		"the inner handler armed on scope open")
	require.True(t, phases[observability.PhaseDisarmed],
		"it disarmed on scope drain")
}

// TestHandlerFreeScopeArmsNothing (SRD-052 NFR-3): a process without any Event
// Sub-Process arms no handler.
func TestHandlerFreeScopeArmsNothing(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("no-handler")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	task := hitTask(t, "task", &atomic.Int32{}, "", 0)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	for _, e := range []flow.Element{start, task, end} {
		require.NoError(t, p.Add(e))
	}
	link(t, start, task)
	link(t, task, end)

	require.Empty(t, runObserved(t, p).handlerPhases(),
		"no event sub-process, no handler fact")
}

// condEventSub builds an Event Sub-Process with a Conditional start whose
// condition is always-true — so it fires at arm time (SRD-052 FR-9).
func condEventSub(t *testing.T, name string, ran *atomic.Int32) *activities.SubProcess {
	t.Helper()

	es, err := activities.NewSubProcess(name, activities.WithTriggeredByEvent())
	require.NoError(t, err)

	alwaysTrue := true
	var evals int
	start, err := events.NewStartEvent(name+"-start",
		events.WithConditionalTrigger(
			mustCondDef(t, condExpr(t, &alwaysTrue, &evals))))
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

// TestConditionalStartHandlerFires (SRD-052 FR-9): a Conditional-start Event
// Sub-Process — the conditional START ADR-006 v.3 deferred to here — arms as a
// loop-local subscription and fires on its true edge (here, at arm time).
func TestConditionalStartHandlerFires(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("cond-handler")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	task := hitTask(t, "task", &atomic.Int32{}, "", 0)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	for _, e := range []flow.Element{start, task, end, condEventSub(t, "cond-es", &atomic.Int32{})} {
		require.NoError(t, p.Add(e))
	}
	link(t, start, task)
	link(t, task, end)

	phases := runObserved(t, p).handlerPhases()
	require.True(t, phases[observability.PhaseArmed], "the conditional handler armed")
	require.True(t, phases[observability.PhaseFired],
		"an arm-time-true condition fires the handler")
}

// errEventSub builds an Event Sub-Process with an Error start — always
// interrupting (§10.5.6), armed as a scope-chain catch, no hub waiter.
func errEventSub(t *testing.T, name, code string) *activities.SubProcess {
	t.Helper()

	es, err := activities.NewSubProcess(name, activities.WithTriggeredByEvent())
	require.NoError(t, err)

	bpErr, err := bpmncommon.NewError(name+"-err", code, nil)
	require.NoError(t, err)
	eed, err := events.NewErrorEventDefinition(bpErr)
	require.NoError(t, err)
	start, err := events.NewStartEvent(name+"-start",
		events.WithErrorTrigger(eed))
	require.NoError(t, err)

	task := hitTask(t, name+"-task", &atomic.Int32{}, "", 0)
	end, err := events.NewEndEvent(name + "-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, task, end} {
		require.NoError(t, es.Add(e))
	}
	linkAll(t, [2]flow.Element{start, task}, [2]flow.Element{task, end})

	return es
}

// TestErrorHandlerArmsWithoutWaiter (SRD-052 FR-5/FR-8): an Error-start Event
// Sub-Process arms as a scope-chain catch — no hub waiter — and disarms with
// the scope.
func TestErrorHandlerArmsWithoutWaiter(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("err-handler")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	task := hitTask(t, "task", &atomic.Int32{}, "", 0)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	for _, e := range []flow.Element{start, task, end, errEventSub(t, "err-es", "E_X")} {
		require.NoError(t, p.Add(e))
	}
	link(t, start, task)
	link(t, task, end)

	phases := runObserved(t, p).handlerPhases()
	require.True(t, phases[observability.PhaseArmed], "the error handler armed")
	require.True(t, phases[observability.PhaseDisarmed], "and disarmed")
}

// TestScopeHandlerWatchHubFire (SRD-052 FR-5): a hub fire delivered to a
// handler's watch (Message/Signal/Timer) emits evScopeHandlerFire to the loop.
func TestScopeHandlerWatchHubFire(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	es := sigEventSub(t, "hub", &atomic.Int32{})
	start, def, ok := triggeredStartOf(es)
	require.True(t, ok)

	inst := &Instance{
		events: make(chan trackEvent, 1), loopDone: make(chan struct{}),
	}
	w := &scopeHandlerWatch{inst: inst, handler: es, start: start, def: def}

	require.Equal(t, start.ID(), w.ID())
	require.NoError(t, w.ProcessEvent(t.Context(), def))

	ev := <-inst.events
	require.Equal(t, evScopeHandlerFire, ev.kind)
	require.Equal(t, es.ID(), ev.node.ID())
}

// TestArmScopeHandlersRegisterError (SRD-052 FR-5): a hub registration failure
// while arming a handler faults the instance rather than parking silently.
func TestArmScopeHandlersRegisterError(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("arm-fail")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	for _, e := range []flow.Element{start, end, sigEventSub(t, "es", &atomic.Int32{})} {
		require.NoError(t, p.Add(e))
	}
	link(t, start, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)
	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		failEventProducer{}, nil)
	require.NoError(t, err)

	runToDone(t, inst)
	require.Equal(t, Terminated, inst.State(),
		"a handler that cannot arm faults the instance")
}

// TestArmScopeHandlersStopping (SRD-052 FR-5): arming is a no-op once the
// instance is stopping.
func TestArmScopeHandlersStopping(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ls := newLoopState(&Instance{})
	ls.stopping = true

	ls.armScopeHandlers(t.Context(),
		[]flow.Node{sigEventSub(t, "es", &atomic.Int32{})}, scope.EmptyDataPath)

	require.Empty(t, ls.scopeHandlers, "nothing armed while stopping")
}

// TestTriggeredStartOfEdgeCases (SRD-052 FR-5): the shape guards — a
// non-container node and a container with no triggered start.
func TestTriggeredStartOfEdgeCases(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	end, err := events.NewEndEvent("plain")
	require.NoError(t, err)
	_, _, ok := triggeredStartOf(end)
	require.False(t, ok, "a non-container node has no triggered start")

	empty, err := activities.NewSubProcess("empty",
		activities.WithTriggeredByEvent())
	require.NoError(t, err)
	_, _, ok = triggeredStartOf(empty)
	require.False(t, ok, "an empty container has no triggered start")
}

// TestApplyRoutesScopeHandlerFire (SRD-052 FR-7): the loop's apply routes an
// evScopeHandlerFire to fireScopeHandler.
func TestApplyRoutesScopeHandlerFire(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	es := sigEventSub(t, "route", &atomic.Int32{})
	start, def, ok := triggeredStartOf(es)
	require.True(t, ok)

	rec := &obsRecorder{}
	inst := &Instance{}
	inst.observers = []obsReg{{fn: rec.record, id: 1}}
	ls := newLoopState(inst)
	ls.scopeHandlers[scope.EmptyDataPath] = []*scopeHandlerWatch{{
		inst: inst, handler: es, start: start, def: def, path: scope.EmptyDataPath,
	}}

	ls.apply(t.Context(), trackEvent{kind: evScopeHandlerFire, node: es})
	require.True(t, rec.handlerPhases()[observability.PhaseFired])
}

// TestScopeHandlerFirePlaceholder (SRD-052 M2): fireScopeHandler records a fire
// for an armed handler and is a no-op for an unarmed one (the late-fire drop) —
// the seam M3 fills with cancel-and-run.
func TestScopeHandlerFirePlaceholder(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	es := sigEventSub(t, "fire", &atomic.Int32{})
	start, _, ok := triggeredStartOf(es)
	require.True(t, ok)

	ls := newLoopState(&Instance{})
	rec := &obsRecorder{}
	inst := &Instance{}
	inst.observers = []obsReg{{fn: rec.record, id: 1}}
	ls.inst = inst

	w := &scopeHandlerWatch{
		inst: inst, handler: es, start: start,
		def: start.Definitions()[0], path: scope.EmptyDataPath,
	}
	ls.scopeHandlers[scope.EmptyDataPath] = []*scopeHandlerWatch{w}

	ls.fireScopeHandler(t.Context(), trackEvent{
		kind: evScopeHandlerFire, node: es,
	})
	require.True(t, rec.handlerPhases()[observability.PhaseFired],
		"an armed handler's fire is recorded")

	// an unarmed handler (unknown node) is a benign no-op.
	other := sigEventSub(t, "other", &atomic.Int32{})
	require.NotPanics(t, func() {
		ls.fireScopeHandler(t.Context(), trackEvent{
			kind: evScopeHandlerFire, node: other,
		})
	})
}
