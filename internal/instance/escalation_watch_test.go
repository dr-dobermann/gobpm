package instance

import (
	"sync/atomic"
	"testing"

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
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/stretchr/testify/require"
)

// SRD-058 M2a — the Escalation throw seam + boundary catch, exercised through
// the real loop. An escalation throw climbs the throwing track's scope chain to
// an Escalation boundary (interrupting cancels the scope, non-interrupting forks
// a parallel token); an escalation with no reachable catcher is logged, never
// faulted.

// escDef builds an EscalationEventDefinition carrying code — the throw code on a
// throw event, the catch code on a boundary. Matching is by code (SRD-058 §4.3).
func escDef(t *testing.T, code string) *events.EscalationEventDefinition {
	t.Helper()

	return events.MustEscalationEventDefinition(
		events.MustEscalation("esc-"+code, code,
			data.MustItemDefinition(values.NewVariable(1))))
}

// escEnd builds an Escalation End Event that raises code and ends its token.
func escEnd(t *testing.T, name, code string) *events.EndEvent {
	t.Helper()

	ee, err := events.NewEndEvent(name,
		events.WithEscalationTrigger(escDef(t, code)))
	require.NoError(t, err)

	return ee
}

// escThrow builds an Escalation Intermediate Throw that raises code and lets its
// token continue on its outgoing flow.
func escThrow(t *testing.T, name, code string) *events.IntermediateThrowEvent {
	t.Helper()

	it, err := events.NewIntermediateThrowEvent(name, escDef(t, code))
	require.NoError(t, err)

	return it
}

// escGuarded builds start → body → normal-end with an Escalation boundary
// catching code on body whose exception flow runs handler → exc-end. interrupting
// selects the boundary mode. It returns the exception- and normal-end ids so a
// test can assert which path ran.
func escGuarded(
	t *testing.T,
	name string,
	body *activities.SubProcess,
	code string,
	interrupting bool,
	handler *activities.ServiceTask,
) (p *process.Process, excEndID, normalEndID string) {
	t.Helper()

	p, err := process.New(name)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	normalEnd, err := events.NewEndEvent("normal-end")
	require.NoError(t, err)

	be, err := events.NewBoundaryEvent("esc-bnd", body,
		escDef(t, code), interrupting)
	require.NoError(t, err)

	excEnd, err := events.NewEndEvent("exc-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, body, normalEnd, be, handler, excEnd} {
		require.NoError(t, p.Add(e))
	}

	linkAll(t,
		[2]flow.Element{start, body},
		[2]flow.Element{body, normalEnd},
		[2]flow.Element{be, handler},
		[2]flow.Element{handler, excEnd})

	return p, excEnd.ID(), normalEnd.ID()
}

// T-2: an Escalation End Event inside a sub-process is caught by an interrupting
// Escalation boundary ON that sub-process — the scope cancels and the exception
// flow runs; the instance completes, it does not fault.
func TestEscalationInterruptingBoundaryCatches(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	body, err := activities.NewSubProcess("body")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	throw := escEnd(t, "throw", "E")
	for _, e := range []flow.Element{sStart, throw} {
		require.NoError(t, body.Add(e))
	}
	linkAll(t, [2]flow.Element{sStart, throw})

	var handled atomic.Int32
	p, excEndID, normalEndID := escGuarded(t, "esc-int", body, "E", true,
		hitTask(t, "handle", &handled, "", 0))

	inst := runInstance(t, p)

	require.Equal(t, Completed, inst.State(),
		"a caught escalation does not fault the instance")
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, handled.Load(), "the exception flow ran")
	require.True(t, reachedNode(inst, excEndID))
	require.False(t, reachedNode(inst, normalEndID),
		"the interrupted sub-process's normal path did not run")
}

// T-3: an Escalation Intermediate Throw is caught by a NON-interrupting boundary —
// a parallel token runs the handler while the sub-process continues its own flow;
// both complete.
func TestEscalationNonInterruptingBoundaryForks(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var charged, handled atomic.Int32

	body, err := activities.NewSubProcess("body")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	throw := escThrow(t, "throw", "E")
	charge := hitTask(t, "charge", &charged, "", 0)
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{sStart, throw, charge, sEnd} {
		require.NoError(t, body.Add(e))
	}
	linkAll(t,
		[2]flow.Element{sStart, throw},
		[2]flow.Element{throw, charge},
		[2]flow.Element{charge, sEnd})

	p, _, _ := escGuarded(t, "esc-non-int", body, "E", false,
		hitTask(t, "handle", &handled, "", 0))

	inst := runInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, charged.Load(),
		"the sub-process continues — a non-interrupting catch does not cancel it")
	require.EqualValues(t, 1, handled.Load(),
		"the non-interrupting boundary runs the handler in parallel")
}

// T-4: an escalation thrown in an inner sub-process with no local catcher
// propagates up to an interrupting Escalation boundary on the OUTER sub-process —
// the innermost matching catcher up the scope chain.
func TestEscalationScopeChainCatches(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	inner, err := activities.NewSubProcess("inner")
	require.NoError(t, err)
	iStart, err := events.NewStartEvent("i-start")
	require.NoError(t, err)
	throw := escEnd(t, "i-throw", "E")
	for _, e := range []flow.Element{iStart, throw} {
		require.NoError(t, inner.Add(e))
	}
	linkAll(t, [2]flow.Element{iStart, throw})

	body, err := activities.NewSubProcess("body")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{sStart, inner, sEnd} {
		require.NoError(t, body.Add(e))
	}
	linkAll(t,
		[2]flow.Element{sStart, inner},
		[2]flow.Element{inner, sEnd})

	var handled atomic.Int32
	p, excEndID, _ := escGuarded(t, "esc-chain", body, "E", true,
		hitTask(t, "handle", &handled, "", 0))

	inst := runInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, handled.Load(),
		"the escalation was caught on the outer boundary, up the scope chain")
	require.True(t, reachedNode(inst, excEndID))
}

// T-6: an escalation with no reachable catcher does NOT fault the instance and
// does not stop execution — but it is logged, emitting a KindEscalation/
// PhaseUnresolved fact (never silently dropped, FR-4).
func TestEscalationUnresolvedLogsNoFault(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	body, err := activities.NewSubProcess("body")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	throw := escEnd(t, "throw", "NOPE")
	for _, e := range []flow.Element{sStart, throw} {
		require.NoError(t, body.Add(e))
	}
	linkAll(t, [2]flow.Element{sStart, throw})

	var after atomic.Int32
	p := wrapSP(t, "esc-unresolved", body, hitTask(t, "after", &after, "", 0))

	s, err := snapshot.New(p)
	require.NoError(t, err)
	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&capturingProducer{procs: map[string]eventproc.EventProcessor{}}, nil)
	require.NoError(t, err)

	rec := &obsRecorder{}
	inst.AddObserver(rec.record)

	runToDone(t, inst)

	require.Equal(t, Completed, inst.State(),
		"an unresolved escalation is non-critical — the instance completes")
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, after.Load(),
		"execution continues past the unresolved throw")
	require.True(t,
		rec.phasesOf(observability.KindEscalation)[observability.PhaseUnresolved],
		"an unresolved escalation is logged (KindEscalation/Unresolved)")
}

// escEventSub builds an event-sub-process whose Escalation start catches code
// and runs a marker task. nonInterrupting selects the start mode.
func escEventSub(
	t *testing.T, name, code string, ran *atomic.Int32, nonInterrupting bool,
) *activities.SubProcess {
	t.Helper()

	es, err := activities.NewSubProcess(name, activities.WithTriggeredByEvent())
	require.NoError(t, err)

	startOpts := []options.Option{events.WithEscalationTrigger(escDef(t, code))}
	if nonInterrupting {
		startOpts = append(startOpts, events.WithNonInterrupting())
	}

	start, err := events.NewStartEvent(name+"-start", startOpts...)
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

// T-5a: an escalation thrown inside a scope is caught by that scope's own
// interrupting Escalation-start Event Sub-Process on the chain walk — the handler
// runs, absorbs the escalation, and the host resumes on its normal flow.
func TestEscalationEventSubCatchesOnChain(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var ran atomic.Int32

	sp, err := activities.NewSubProcess("ebody")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("e-start")
	require.NoError(t, err)
	throw := escThrow(t, "throw", "E9")
	sEnd, err := events.NewEndEvent("e-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{
		sStart, throw, sEnd, escEventSub(t, "eh", "E9", &ran, false),
	} {
		require.NoError(t, sp.Add(e))
	}
	linkAll(t, [2]flow.Element{sStart, throw}, [2]flow.Element{throw, sEnd})

	var after atomic.Int32
	inst := runInstance(t,
		wrapSP(t, "esc-eventsub", sp, hitTask(t, "after", &after, "", 0)))

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, ran.Load(), "the escalation handler ran")
	require.EqualValues(t, 1, after.Load(), "the host resumed on its normal flow")
}

// T-5b: a NON-interrupting Escalation-start Event Sub-Process runs concurrently
// with the scope's own flow — the scope is not cancelled, so both complete.
func TestEscalationEventSubNonInterrupting(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var ran, charged atomic.Int32

	sp, err := activities.NewSubProcess("ebody")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("e-start")
	require.NoError(t, err)
	throw := escThrow(t, "throw", "E9")
	charge := hitTask(t, "charge", &charged, "", 0)
	sEnd, err := events.NewEndEvent("e-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{
		sStart, throw, charge, sEnd, escEventSub(t, "eh", "E9", &ran, true),
	} {
		require.NoError(t, sp.Add(e))
	}
	linkAll(t,
		[2]flow.Element{sStart, throw},
		[2]flow.Element{throw, charge},
		[2]flow.Element{charge, sEnd})

	var after atomic.Int32
	inst := runInstance(t,
		wrapSP(t, "esc-eventsub-ni", sp, hitTask(t, "after", &after, "", 0)))

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, ran.Load(),
		"the non-interrupting handler ran concurrently")
	require.EqualValues(t, 1, charged.Load(),
		"the scope's own flow continued — no cancel")
	require.EqualValues(t, 1, after.Load())
}

// T-5c: with BOTH an event-sub Escalation handler and an Escalation boundary in
// the same scope, the inline handler wins (the innermost catcher, §10.5.6).
func TestEscalationHandlerBeatsBoundary(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var handlerRan, boundaryRan atomic.Int32

	sp, err := activities.NewSubProcess("ebody")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("e-start")
	require.NoError(t, err)
	throw := escEnd(t, "throw", "E9")
	for _, e := range []flow.Element{
		sStart, throw, escEventSub(t, "eh", "E9", &handlerRan, false),
	} {
		require.NoError(t, sp.Add(e))
	}
	linkAll(t, [2]flow.Element{sStart, throw})

	// an interrupting Escalation boundary on the same sub-process also matches E9.
	p, _, _ := escGuarded(t, "esc-precedence", sp, "E9", true,
		hitTask(t, "boundary", &boundaryRan, "", 0))

	inst := runInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, handlerRan.Load(),
		"the inline event-sub handler catches (innermost)")
	require.EqualValues(t, 0, boundaryRan.Load(),
		"the boundary does not fire — the handler absorbed the escalation")
}

// TestExecEnvEscalateNilTrack: a track-less frame has no scope to escalate from,
// so Escalate is a no-op — it returns before emit (which would need a live loop)
// and never panics (SRD-058 §3.1).
func TestExecEnvEscalateNilTrack(t *testing.T) {
	ee := newExecEnv(&Instance{}, nil, nil)

	require.NotPanics(t, func() { ee.Escalate("E") })
}

// TestEscalationHandlerBudgetSpentPropagates: an interrupting Escalation handler
// whose scope's interrupting budget is already spent does NOT catch — the
// escalation keeps propagating to an outer catcher (§10.5.6, SRD-058 FR-6).
func TestEscalationHandlerBudgetSpentPropagates(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	inst, ls := openInstance(t)

	es := escEventSub(t, "eh", "E9", &atomic.Int32{}, false) // interrupting
	_, def, ok := triggeredStartOf(es)
	require.True(t, ok)

	path := inst.sc.root
	ls.scopeHandlers[path] = []*scopeHandlerWatch{{
		inst: inst, handler: es, def: def, path: path, interrupting: true,
	}}
	ls.scopeInterrupted[path] = true // the budget is already spent

	require.False(t, ls.catchEscalationHandler(t.Context(), path, "E9"),
		"a spent-budget interrupting handler does not catch")
}

// TestEscalationBoundaryOnNonHost: escalationBoundaryOn returns nil for a node
// that cannot host boundaries (a plain flow node in the scope chain), the
// defensive guard the scope walk relies on (SRD-058 FR-2).
func TestEscalationBoundaryOnNonHost(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	start, err := events.NewStartEvent("s")
	require.NoError(t, err)

	require.Nil(t, escalationBoundaryOn(start, "E"))
}
