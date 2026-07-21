package instance

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/stretchr/testify/require"
)

// SRD-059 M3 — the compensation throw and the sweep: targeted + scope-wide
// reverse order, the snapshot read surface, wait semantics, unresolved-logs,
// handler failure on the Error chain, and event-sub handler consumption.

// readingHandler builds an isForCompensation ServiceTask that records the
// value of datum name it observes through its DataReader (the snapshot
// read-surface probe) and its invocation order.
func readingHandler(
	t *testing.T, hname, dname string, saw *atomic.Int64, seq *atomic.Int64,
	order *atomic.Int64,
) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New(hname+"-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			if dname != "" {
				d, err := r.GetData(dname)
				if err != nil {
					return nil, err
				}

				if v, ok := d.Value().Get(ctx).(int); ok {
					saw.Store(int64(v))
				}
			}

			if order != nil {
				order.Store(seq.Add(1))
			}

			return nil, nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(hname, op,
		activities.WithoutParams(), activities.WithCompensation())
	require.NoError(t, err)

	return st
}

// guardWith attaches a Compensation boundary on host routing to handler,
// returning the nodes to Add.
func guardWith(
	t *testing.T, host flow.ActivityNode, handler *activities.ServiceTask,
) []flow.Element {
	t.Helper()

	ced, err := events.NewCompensationEventDefinition(nil, true)
	require.NoError(t, err)
	bnd, err := events.NewCompensationBoundaryEvent(
		"comp-"+handler.Name(), host, ced, handler)
	require.NoError(t, err)

	return []flow.Element{bnd, handler}
}

// compThrow builds an Intermediate Throw with a Compensation definition.
func compThrow(
	t *testing.T, name string, target flow.ActivityNode, wait bool,
) *events.IntermediateThrowEvent {
	t.Helper()

	ced, err := events.NewCompensationEventDefinition(target, wait)
	require.NoError(t, err)
	it, err := events.NewIntermediateThrowEvent(name, ced)
	require.NoError(t, err)

	return it
}

// T-2: a targeted throw runs the handler against the SNAPSHOT — the handler
// reads the value the activity completed with, not the mutated live one.
func TestCompensateTargetedReadsSnapshot(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var aRan, setRan atomic.Int32
	var saw, seq atomic.Int64

	p, err := process.New("comp-targeted")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	setX1 := hitTask(t, "setX1", &setRan, "x", 1)
	a := hitTask(t, "A", &aRan, "", 0)
	setX2 := hitTask(t, "setX2", &setRan, "x", 42)
	throw := compThrow(t, "throw", a, true)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	undoA := readingHandler(t, "undoA", "x", &saw, &seq, nil)

	for _, e := range append([]flow.Element{
		start, setX1, a, setX2, throw, end,
	}, guardWith(t, a, undoA)...) {
		require.NoError(t, p.Add(e))
	}
	linkAll(t,
		[2]flow.Element{start, setX1},
		[2]flow.Element{setX1, a},
		[2]flow.Element{a, setX2},
		[2]flow.Element{setX2, throw},
		[2]flow.Element{throw, end})

	inst, rec := observeInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, saw.Load(),
		"the handler saw x as A completed it (snapshot), not the live 42")

	require.Len(t, compFacts(rec, observability.PhaseCompensating), 1)
	require.Len(t, compFacts(rec, observability.PhaseCompensated), 1)
	require.Len(t, compFacts(rec, observability.PhaseThrown), 1)
}

// T-3 + T-4(wait=true): a scope-wide End-Event throw compensates B then A
// (reverse completion order), sequentially, with the thrower parked until
// both handlers complete.
func TestCompensateScopeWideReverseOrder(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var aRan, bRan atomic.Int32
	var seq, orderA, orderB atomic.Int64

	p, err := process.New("comp-reverse")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	a := hitTask(t, "A", &aRan, "", 0)
	b := hitTask(t, "B", &bRan, "", 0)

	ced, err := events.NewCompensationEventDefinition(nil, true)
	require.NoError(t, err)
	throwEnd, err := events.NewEndEvent("comp-end",
		events.WithCompensationTrigger(ced))
	require.NoError(t, err)

	undoA := readingHandler(t, "undoA", "", nil, &seq, &orderA)
	undoB := readingHandler(t, "undoB", "", nil, &seq, &orderB)

	for _, e := range append(append([]flow.Element{start, a, b, throwEnd},
		guardWith(t, a, undoA)...), guardWith(t, b, undoB)...) {
		require.NoError(t, p.Add(e))
	}
	linkAll(t, [2]flow.Element{start, a}, [2]flow.Element{a, b},
		[2]flow.Element{b, throwEnd})

	inst, rec := observeInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, orderB.Load(), "B compensates first (reverse)")
	require.EqualValues(t, 2, orderA.Load(), "A compensates second")

	comps := compFacts(rec, observability.PhaseCompensating)
	require.Len(t, comps, 2)
	require.Equal(t, "B", comps[0].NodeName)
	require.Equal(t, "A", comps[1].NodeName)
}

// T-4 (wait=true ordering): the token after a wait-for-completion throw runs
// only after the handlers completed.
func TestCompensateWaitParksThrower(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var aRan atomic.Int32
	var seq, orderUndo, orderAfter atomic.Int64

	p, err := process.New("comp-wait")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	a := hitTask(t, "A", &aRan, "", 0)
	throw := compThrow(t, "throw", nil, true) // scope-wide, wait

	afterOp, err := gooper.New("after-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			orderAfter.Store(seq.Add(1))

			return nil, nil
		})
	require.NoError(t, err)
	after, err := activities.NewServiceTask("after", afterOp,
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	undoA := readingHandler(t, "undoA", "", nil, &seq, &orderUndo)

	for _, e := range append([]flow.Element{start, a, throw, after, end},
		guardWith(t, a, undoA)...) {
		require.NoError(t, p.Add(e))
	}
	linkAll(t, [2]flow.Element{start, a}, [2]flow.Element{a, throw},
		[2]flow.Element{throw, after}, [2]flow.Element{after, end})

	inst, _ := observeInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, orderUndo.Load(), "the handler ran first")
	require.EqualValues(t, 2, orderAfter.Load(),
		"the after-throw token waited for the sweep")
}

// T-4 (wait=false): a fire-and-forget throw does not park; the instance
// completes with the handler run.
func TestCompensateFireAndForget(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var aRan atomic.Int32
	var seq, orderUndo atomic.Int64

	p, err := process.New("comp-ff")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	a := hitTask(t, "A", &aRan, "", 0)
	throw := compThrow(t, "throw", nil, false)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	undoA := readingHandler(t, "undoA", "", nil, &seq, &orderUndo)

	for _, e := range append([]flow.Element{start, a, throw, end},
		guardWith(t, a, undoA)...) {
		require.NoError(t, p.Add(e))
	}
	linkAll(t, [2]flow.Element{start, a}, [2]flow.Element{a, throw},
		[2]flow.Element{throw, end})

	inst, rec := observeInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, orderUndo.Load(), "the handler ran")
	require.Len(t, compFacts(rec, observability.PhaseCompensated), 1)
}

// T-6: an unresolved throw (empty ledger) is logged — no fault, execution
// continues past the wait.
func TestCompensateUnresolvedLogsNoFault(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var aRan, afterRan atomic.Int32

	p, err := process.New("comp-unresolved")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	a := hitTask(t, "A", &aRan, "", 0) // unguarded — never ledgers
	throw := compThrow(t, "throw", nil, true)
	after := hitTask(t, "after", &afterRan, "", 0)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, a, throw, after, end} {
		require.NoError(t, p.Add(e))
	}
	linkAll(t, [2]flow.Element{start, a}, [2]flow.Element{a, throw},
		[2]flow.Element{throw, after}, [2]flow.Element{after, end})

	inst, rec := observeInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, afterRan.Load(), "execution continued")
	require.Len(t, compFacts(rec, observability.PhaseThrown), 1)
	require.Len(t, compFacts(rec, observability.PhaseUnresolved), 1)
	require.Empty(t, compFacts(rec, observability.PhaseCompensating))
}

// T-9: a failing handler aborts the sweep and faults through the Error chain
// — the thrower resumes (no hang) and the uncaught fault terminates the
// instance with the handler's code.
func TestCompensateHandlerFailureFaults(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var aRan atomic.Int32

	p, err := process.New("comp-hfail")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	a := hitTask(t, "A", &aRan, "", 0)
	throw := compThrow(t, "throw", a, true)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	boomOp, err := gooper.New("undo-boom-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return nil, &events.BpmnError{Code: "CF"}
		})
	require.NoError(t, err)
	undoBoom, err := activities.NewServiceTask("undo-boom", boomOp,
		activities.WithoutParams(), activities.WithCompensation())
	require.NoError(t, err)

	for _, e := range append([]flow.Element{start, a, throw, end},
		guardWith(t, a, undoBoom)...) {
		require.NoError(t, p.Add(e))
	}
	linkAll(t, [2]flow.Element{start, a}, [2]flow.Element{a, throw},
		[2]flow.Element{throw, end})

	inst, _ := observeInstance(t, p)

	require.Equal(t, Terminated, inst.State(),
		"the handler's fault terminated the instance (uncaught)")

	var be *events.BpmnError
	require.ErrorAs(t, inst.LastErr(), &be)
	require.Equal(t, "CF", be.Code)
}

// T-5 (consume half): compensating a completed Sub-Process runs its own
// compensation Event Sub-Process handler against the folded entry.
func TestCompensateSubProcessRunsEventSub(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var w1Ran, esRan atomic.Int32

	// the SP's compensation handler (event sub-process, Compensation start).
	es, err := activities.NewSubProcess("sp-comp",
		activities.WithTriggeredByEvent())
	require.NoError(t, err)
	cedS, err := events.NewCompensationEventDefinition(nil, true)
	require.NoError(t, err)
	cStart, err := events.NewStartEvent("c-start",
		events.WithCompensationTrigger(cedS))
	require.NoError(t, err)
	cTask := hitTask(t, "c-task", &esRan, "", 0)
	cEnd, err := events.NewEndEvent("c-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{cStart, cTask, cEnd} {
		require.NoError(t, es.Add(e))
	}
	linkAll(t, [2]flow.Element{cStart, cTask}, [2]flow.Element{cTask, cEnd})

	// body: s-start → w1 → s-end, PLUS the handler.
	sp, err := activities.NewSubProcess("body")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	w1 := hitTask(t, "w1", &w1Ran, "", 0)
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{sStart, w1, sEnd, es} {
		require.NoError(t, sp.Add(e))
	}
	linkAll(t, [2]flow.Element{sStart, w1}, [2]flow.Element{w1, sEnd})

	// process: start → sp → throw(ref=sp, wait) → end.
	p, err := process.New("comp-eventsub")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	throw := compThrow(t, "throw", sp, true)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	for _, e := range []flow.Element{start, sp, throw, end} {
		require.NoError(t, p.Add(e))
	}
	linkAll(t, [2]flow.Element{start, sp}, [2]flow.Element{sp, throw},
		[2]flow.Element{throw, end})

	inst, rec := observeInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, esRan.Load(),
		"the SP's compensation event-sub handler ran")

	comped := compFacts(rec, observability.PhaseCompensated)
	require.Len(t, comped, 1)
	require.Equal(t, "body", comped[0].NodeName)
}

// TestCompensateTargetedUnresolved: a targeted throw at a never-completed
// activity resolves to nothing — logged with the target ref, no fault.
func TestCompensateTargetedUnresolved(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var aRan, afterRan atomic.Int32

	p, err := process.New("comp-targeted-unres")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	a := hitTask(t, "A", &aRan, "", 0)
	after := hitTask(t, "after", &afterRan, "", 0)
	// the throw targets `after`, which has not completed at throw time.
	throw := compThrow(t, "throw", after, true)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, a, throw, after, end} {
		require.NoError(t, p.Add(e))
	}
	linkAll(t, [2]flow.Element{start, a}, [2]flow.Element{a, throw},
		[2]flow.Element{throw, after}, [2]flow.Element{after, end})

	inst, rec := observeInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.EqualValues(t, 1, afterRan.Load())

	unres := compFacts(rec, observability.PhaseUnresolved)
	require.Len(t, unres, 1)
	require.Equal(t, after.ID(), unres[0].Details["activity_ref"])
}

// TestExtractLedgerEntry (white-box): the targeted extraction removes the
// entry wherever it sits — top level or nested in a folded child — and
// reports a miss with the book intact.
func TestExtractLedgerEntry(t *testing.T) {
	inner := &ledgerEntry{activityID: "inner", handlerID: "h-inner"}
	sp := &ledgerEntry{activityID: "sp", folded: []*ledgerEntry{inner}}
	top := &ledgerEntry{activityID: "top", handlerID: "h-top"}
	book := []*ledgerEntry{sp, top}

	rest, found := extractLedgerEntry(book, "top")
	require.NotNil(t, found)
	require.Equal(t, "top", found.activityID)
	require.Len(t, rest, 1)

	rest, found = extractLedgerEntry(book, "inner")
	require.NotNil(t, found)
	require.Equal(t, "inner", found.activityID)
	require.Empty(t, sp.folded, "the nested entry was consumed")
	require.Len(t, rest, 2, "the top level is untouched by a nested hit")

	rest, found = extractLedgerEntry(book, "nope")
	require.Nil(t, found)
	require.Len(t, rest, 2)
}

// TestRunNextCompensationHandlerMissing (white-box): a ledger entry whose
// handler is not in the instance graph is an invariant violation — fail loud.
func TestRunNextCompensationHandlerMissing(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	inst, ls := openInstance(t)

	ls.runNextCompensation(t.Context(), &compSweep{
		path: inst.sc.root,
		queue: []*ledgerEntry{{
			activityID: "x", activityName: "x",
			handlerID: "not-there", handlerName: "ghost",
		}},
	})

	require.True(t, ls.stopping)
	require.Error(t, inst.LastErr())
}

// TestExecEnvCompensateNilTrack: a track-less frame has no scope to compensate
// from — a no-op, never a panic (the Escalate precedent).
func TestExecEnvCompensateNilTrack(t *testing.T) {
	ee := newExecEnv(&Instance{}, nil, nil)

	require.NotPanics(t, func() { ee.Compensate("x", false) })
}

// TestCompensationDoneSentinel (white-box): the sentinel's identity surface.
func TestCompensationDoneSentinel(t *testing.T) {
	cd := newCompensationDone()
	require.Equal(t, compensationDoneTrigger, cd.Type())
	require.NotEmpty(t, cd.ID())
}

// TestCompensationNodeByIDNested (white-box): the handler lookup descends
// composite-in-composite graphs and reports a miss across them.
func TestCompensationNodeByIDNested(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	_, ls := openInstance(t)

	deep := hitTask(t, "deep", &atomic.Int32{}, "", 0)

	inner, err := activities.NewSubProcess("inner-nb")
	require.NoError(t, err)
	require.NoError(t, inner.Add(deep))

	outer, err := activities.NewSubProcess("outer-nb")
	require.NoError(t, err)
	require.NoError(t, outer.Add(inner))

	ls.inst.s.Nodes[outer.ID()] = outer

	n, ok := ls.compensationNodeByID(deep.ID())
	require.True(t, ok, "found two levels down")
	require.Equal(t, deep.ID(), n.ID())

	_, ok = ls.compensationNodeByID("missing-nb")
	require.False(t, ok, "a miss across composites")
}
