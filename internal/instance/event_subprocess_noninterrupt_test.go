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
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/stretchr/testify/require"
)

// nonIntrCondEventSub builds a NON-interrupting Conditional-start Event
// Sub-Process whose always-true condition fires at arm time (deterministic, no
// external trigger).
func nonIntrCondEventSub(
	t *testing.T, name string, ran *atomic.Int32,
) *activities.SubProcess {
	t.Helper()

	es, err := activities.NewSubProcess(name, activities.WithTriggeredByEvent())
	require.NoError(t, err)

	alwaysTrue := true
	var evals int
	start, err := events.NewStartEvent(name+"-start",
		events.WithConditionalTrigger(
			mustCondDef(t, condExpr(t, &alwaysTrue, &evals))),
		events.WithNonInterrupting())
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

// nonIntrSigEventSub builds a NON-interrupting Signal-triggered Event
// Sub-Process (fired externally via the hub, multi-shot).
func nonIntrSigEventSub(
	t *testing.T, name string, ran *atomic.Int32,
) *activities.SubProcess {
	t.Helper()

	es, err := activities.NewSubProcess(name, activities.WithTriggeredByEvent())
	require.NoError(t, err)

	sig, err := events.NewSignal(name+"-sig",
		data.MustItemDefinition(values.NewVariable(1)))
	require.NoError(t, err)
	start, err := events.NewStartEvent(name+"-start",
		[]options.Option{
			events.WithSignalTrigger(events.MustSignalEventDefinition(sig)),
			events.WithNonInterrupting(),
		}...)
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

// TestNonInterruptingConcurrentRun (SRD-053 FR-3): a non-interrupting handler
// fires and runs alongside the scope's normal work — the sibling task is NOT
// cancelled (both run), and the scope completes when both drain.
func TestNonInterruptingConcurrentRun(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var mainRan, handlerRan atomic.Int32

	body, err := activities.NewSubProcess("body")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	mainTask := hitTask(t, "main", &mainRan, "", 0)
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{
		sStart, mainTask, sEnd, nonIntrCondEventSub(t, "h", &handlerRan),
	} {
		require.NoError(t, body.Add(e))
	}
	linkAll(t, [2]flow.Element{sStart, mainTask}, [2]flow.Element{mainTask, sEnd})

	var after atomic.Int32
	inst := runInstance(t,
		wrapSP(t, "ni-concurrent", body, hitTask(t, "after", &after, "", 0)))

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, mainRan.Load(),
		"the sibling task ran — a non-interrupting handler does not cancel it")
	require.EqualValues(t, 1, handlerRan.Load(), "the handler ran concurrently")
	require.EqualValues(t, 1, after.Load(), "the host resumed after both drained")
}

// TestNonInterruptingMultiFire (SRD-053 FR-4/FR-5): a non-interrupting handler
// fired twice spawns TWO concurrent instances in DISTINCT child scopes (not
// serialized by the re-entry queue) — the watch stays armed between fires.
func TestNonInterruptingMultiFire(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var handlerRan atomic.Int32
	es := nonIntrSigEventSub(t, "h", &handlerRan)
	body := scopedSP(t, "body", es) // s-start → stuck (blocks) → s-end + handler

	p := wrapSP(t, "ni-multifire", body, hitTask(t, "after", &atomic.Int32{}, "", 0))
	s, err := snapshot.New(p)
	require.NoError(t, err)

	cp := &capturingProducer{procs: map[string]eventproc.EventProcessor{}}
	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), cp, nil)
	require.NoError(t, err)
	rec := &obsRecorder{}
	inst.AddObserver(rec.record)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	var w *scopeHandlerWatch
	require.Eventually(t, func() bool { w = cp.scopeWatch(); return w != nil },
		3*time.Second, 5*time.Millisecond)

	require.NoError(t, w.ProcessEvent(ctx, w.def))
	require.NoError(t, w.ProcessEvent(ctx, w.def))

	require.Eventually(t, func() bool { return handlerRan.Load() == 2 },
		3*time.Second, 5*time.Millisecond,
		"both non-interrupting fires run a handler instance")

	// the two instances opened DISTINCT child scopes for the same handler node.
	require.GreaterOrEqual(t, len(distinctHandlerScopes(rec, "h")), 2,
		"concurrent instances get unique scope paths, not one queued path")
}

// TestRunNonInterruptingHandlerErrorPaths (SRD-053 FR-3): the concurrent-run's
// defensive guards fault the instance — a payload that cannot bind (its
// enclosing scope is not open) and a handler node that is not a NodeExecutor.
func TestRunNonInterruptingHandlerErrorPaths(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("payload bind failure faults", func(t *testing.T) {
		inst, ls := openInstance(t)
		es := nonIntrSigEventSub(t, "e", &atomic.Int32{})
		_, def, ok := triggeredStartOf(es) // the signal def carries an item
		require.True(t, ok)

		unopened, err := inst.sc.root.Append("never-opened")
		require.NoError(t, err)
		w := &scopeHandlerWatch{inst: inst, handler: es, path: unopened}

		ls.runNonInterruptingHandler(t.Context(), w, def)

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

		ls.runNonInterruptingHandler(t.Context(), w, nil)

		require.True(t, ls.stopping)
		require.Error(t, inst.LastErr())
	})
}

// distinctHandlerScopes returns the distinct scope paths a handler node's scope
// opened under (the KindScope Opened facts for that node name).
func distinctHandlerScopes(r *obsRecorder, node string) map[string]bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := map[string]bool{}
	for _, e := range r.events {
		if e.Kind == observability.KindScope &&
			e.Phase == observability.PhaseOpened && e.NodeName == node {
			out[e.Details[observability.AttrScopePath]] = true
		}
	}

	return out
}

// TestNonInterruptingDoesNotCancelScope (SRD-053 FR-3): a non-interrupting fire
// runs the handler but does NOT cancel the scope's blocked sibling — so the
// scope stays open and the instance keeps running (an INTERRUPTING fire would
// have cancelled the sibling and let the instance complete). The shared
// interrupting budget is left unspent by construction (this path never touches
// it).
func TestNonInterruptingDoesNotCancelScope(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var handlerRan atomic.Int32
	es := nonIntrSigEventSub(t, "h", &handlerRan)
	body := scopedSP(t, "body", es) // s-start → stuck (blocks) → s-end + handler
	p := wrapSP(t, "ni-nocancel", body, hitTask(t, "after", &atomic.Int32{}, "", 0))
	s, err := snapshot.New(p)
	require.NoError(t, err)

	cp := &capturingProducer{procs: map[string]eventproc.EventProcessor{}}
	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), cp, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	var w *scopeHandlerWatch
	require.Eventually(t, func() bool { w = cp.scopeWatch(); return w != nil },
		3*time.Second, 5*time.Millisecond)
	require.False(t, w.interrupting, "the handler is non-interrupting")

	require.NoError(t, w.ProcessEvent(ctx, w.def))

	require.Eventually(t, func() bool { return handlerRan.Load() == 1 },
		3*time.Second, 5*time.Millisecond, "the handler ran")
	// the blocked sibling was NOT cancelled → the scope stays open → the
	// instance is still running (an interrupting fire would have completed it).
	require.Equal(t, Active, inst.State(),
		"a non-interrupting fire leaves the scope's blocked work running")
}
