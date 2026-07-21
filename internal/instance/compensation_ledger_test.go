package instance

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
)

// SRD-059 M2 — the completion ledger: Eligible/Folded/Discarded lifecycle,
// presumed abort, and the pay-for-use guarantee.

// compHandler builds an isForCompensation ServiceTask counting its runs.
func compHandler(t *testing.T, name string, ran *atomic.Int32) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New(name+"-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			ran.Add(1)

			return nil, nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(name, op,
		activities.WithoutParams(), activities.WithCompensation())
	require.NoError(t, err)

	return st
}

// guard attaches a Compensation boundary routing to a fresh handler, returning
// the container nodes to Add (the boundary and the handler are both container
// nodes — like every boundary, they must be Added or the per-instance clone
// carries no attachment).
func guard(t *testing.T, host flow.ActivityNode, handlerName string,
	ran *atomic.Int32,
) []flow.Element {
	t.Helper()

	h := compHandler(t, handlerName, ran)
	ced, err := events.NewCompensationEventDefinition(nil, true)
	require.NoError(t, err)
	bnd, err := events.NewCompensationBoundaryEvent(
		"comp-"+handlerName, host, ced, h)
	require.NoError(t, err)

	return []flow.Element{bnd, h}
}

// observeInstance builds and runs p with an obsRecorder attached.
func observeInstance(t *testing.T, p *process.Process) (*Instance, *obsRecorder) {
	t.Helper()

	s, err := snapshot.New(p)
	require.NoError(t, err)
	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&capturingProducer{procs: map[string]eventproc.EventProcessor{}}, nil)
	require.NoError(t, err)

	rec := &obsRecorder{}
	inst.AddObserver(rec.record)

	runToDone(t, inst)

	return inst, rec
}

// compFacts filters the recorded Compensation facts by phase.
func compFacts(rec *obsRecorder, phase observability.Phase) []observability.Fact {
	rec.mu.Lock()
	defer rec.mu.Unlock()

	out := []observability.Fact{}
	for _, f := range rec.events {
		if f.Kind == observability.KindCompensation && f.Phase == phase {
			out = append(out, f)
		}
	}

	return out
}

// T-3's record half + T-8: two guarded leaves ledger Eligible in completion
// order (ordinals 0,1), and — never compensated — discard when the instance
// (their enclosing scope) finishes.
func TestLedgerRecordsInCompletionOrder(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var aRan, bRan, undoRan atomic.Int32

	p, err := process.New("ledger-order")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	a := hitTask(t, "A", &aRan, "", 0)
	b := hitTask(t, "B", &bRan, "", 0)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	gA := guard(t, a, "undoA", &undoRan)
	gB := guard(t, b, "undoB", &undoRan)

	for _, e := range append([]flow.Element{start, a, b, end},
		append(gA, gB...)...) {
		require.NoError(t, p.Add(e))
	}
	linkAll(t, [2]flow.Element{start, a}, [2]flow.Element{a, b},
		[2]flow.Element{b, end})

	inst, rec := observeInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.EqualValues(t, 0, undoRan.Load(), "no compensation was thrown")

	el := compFacts(rec, observability.PhaseEligible)
	require.Len(t, el, 2)
	require.Equal(t, "A", el[0].NodeName)
	require.Equal(t, "0", el[0].Details[observability.AttrOrdinal])
	require.Equal(t, "B", el[1].NodeName)
	require.Equal(t, "1", el[1].Details[observability.AttrOrdinal])

	disc := compFacts(rec, observability.PhaseDiscarded)
	require.Len(t, disc, 2, "both un-compensated entries discarded at the end")
}

// T-7: a failed activity never ledgers — the presumed-abort filter. The
// activity carries BOTH an Error boundary (so the instance survives) and a
// Compensation boundary; failing, it must not become Eligible.
func TestLedgerPresumedAbort(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var undoRan, caught atomic.Int32

	p, err := process.New("ledger-abort")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	boom := throwTask(t, "boom", "E1")
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	g := guard(t, boom, "undo", &undoRan)

	errDefE1 := errDef(t, "e1", "E1")
	errBnd, err := events.NewBoundaryEvent("err-bnd", boom, errDefE1, true)
	require.NoError(t, err)
	handle := hitTask(t, "handle", &caught, "", 0)
	excEnd, err := events.NewEndEvent("exc-end")
	require.NoError(t, err)

	for _, e := range append([]flow.Element{
		start, boom, end, errBnd, handle, excEnd,
	}, g...) {
		require.NoError(t, p.Add(e))
	}
	linkAll(t, [2]flow.Element{start, boom}, [2]flow.Element{boom, end},
		[2]flow.Element{errBnd, handle}, [2]flow.Element{handle, excEnd})

	inst, rec := observeInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.EqualValues(t, 1, caught.Load(), "the error was caught")
	require.Empty(t, compFacts(rec, observability.PhaseEligible),
		"a failed activity never enters the ledger")
}

// T-5 (record/fold half): a completed Sub-Process with an inner guarded leaf
// and its own compensation Event Sub-Process folds the child ledger into the
// parent's entry — Eligible (inner, then the SP itself) and Folded facts.
func TestLedgerFoldsChildScope(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var w1Ran, undoRan, esRan, afterRan atomic.Int32

	// the SP's own compensation handler: an event sub-process with a
	// Compensation start.
	es, err := activities.NewSubProcess("sp-comp",
		activities.WithTriggeredByEvent())
	require.NoError(t, err)
	ced, err := events.NewCompensationEventDefinition(nil, true)
	require.NoError(t, err)
	cStart, err := events.NewStartEvent("c-start",
		events.WithCompensationTrigger(ced))
	require.NoError(t, err)
	cTask := hitTask(t, "c-task", &esRan, "", 0)
	cEnd, err := events.NewEndEvent("c-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{cStart, cTask, cEnd} {
		require.NoError(t, es.Add(e))
	}
	linkAll(t, [2]flow.Element{cStart, cTask}, [2]flow.Element{cTask, cEnd})

	// body: s-start → w1 (guarded) → s-end, PLUS the handler.
	sp, err := activities.NewSubProcess("body")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	w1 := hitTask(t, "w1", &w1Ran, "", 0)
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)
	g1 := guard(t, w1, "undo1", &undoRan)
	for _, e := range append([]flow.Element{sStart, w1, sEnd, es}, g1...) {
		require.NoError(t, sp.Add(e))
	}
	linkAll(t, [2]flow.Element{sStart, w1}, [2]flow.Element{w1, sEnd})

	inst, rec := observeInstance(t,
		wrapSP(t, "ledger-fold", sp, hitTask(t, "after", &afterRan, "", 0)))

	require.Equal(t, Completed, inst.State())

	el := compFacts(rec, observability.PhaseEligible)
	require.Len(t, el, 2, "the inner leaf, then the Sub-Process itself")
	require.Equal(t, "w1", el[0].NodeName)
	require.Equal(t, "body", el[1].NodeName)

	fold := compFacts(rec, observability.PhaseFolded)
	require.Len(t, fold, 1)
	require.Equal(t, "body", fold[0].NodeName)
	require.Equal(t, "1", fold[0].Details["folded_entries"])
}

// A canceled scope's ledger discards (the eligibility window dies with the
// scope): an interrupting Escalation boundary cancels the sub-process whose
// inner leaf had already ledgered.
func TestLedgerDiscardsOnScopeCancel(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var w1Ran, undoRan, handled atomic.Int32

	sp, err := activities.NewSubProcess("body")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	w1 := hitTask(t, "w1", &w1Ran, "", 0)
	// an Escalation End Event raises "X" caught by the interrupting boundary
	// on the sub-process — cancels the scope after w1 ledgered.
	esc := events.MustEscalationEventDefinition(
		events.MustEscalation("esc-x", "X",
			data.MustItemDefinition(nil)))
	throw, err := events.NewEndEvent("throw",
		events.WithEscalationTrigger(esc))
	require.NoError(t, err)
	g1 := guard(t, w1, "undo1", &undoRan)
	for _, e := range append([]flow.Element{sStart, w1, throw}, g1...) {
		require.NoError(t, sp.Add(e))
	}
	linkAll(t, [2]flow.Element{sStart, w1}, [2]flow.Element{w1, throw})

	p, err := process.New("ledger-cancel")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	escC := events.MustEscalationEventDefinition(
		events.MustEscalation("esc-c", "X",
			data.MustItemDefinition(nil)))
	bnd, err := events.NewBoundaryEvent("esc-bnd", sp, escC, true)
	require.NoError(t, err)
	handle := hitTask(t, "handle", &handled, "", 0)
	excEnd, err := events.NewEndEvent("exc-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{start, sp, end, bnd, handle, excEnd} {
		require.NoError(t, p.Add(e))
	}
	linkAll(t, [2]flow.Element{start, sp}, [2]flow.Element{sp, end},
		[2]flow.Element{bnd, handle}, [2]flow.Element{handle, excEnd})

	inst, rec := observeInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.EqualValues(t, 1, handled.Load(), "the escalation was caught")

	disc := compFacts(rec, observability.PhaseDiscarded)
	require.NotEmpty(t, disc, "the canceled scope's ledger discarded")
	require.Equal(t, "w1", disc[0].NodeName)
	require.EqualValues(t, 0, undoRan.Load())
}

// TestAppendLedgerEntrySnapshotFailure (white-box): a snapshot failure at
// ledger-append is an invariant violation — the entry's future handler could
// not be guaranteed its read surface — so the instance fails loudly.
func TestAppendLedgerEntrySnapshotFailure(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	inst, ls := openInstance(t)

	// a path outside the instance's data plane — SnapshotAt rejects it.
	outside, err := scope.NewDataPath("/elsewhere")
	require.NoError(t, err)

	ls.appendLedgerEntry(outside, &ledgerEntry{
		activityID: "x", activityName: "x",
	}, nil)

	require.True(t, ls.stopping)
	require.Error(t, inst.LastErr())
}

// NFR-2 pay-for-use: a model with no compensation handlers emits no
// Compensation facts at all.
func TestLedgerPayForUse(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var aRan atomic.Int32

	p, err := process.New("ledger-none")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	a := hitTask(t, "A", &aRan, "", 0)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	for _, e := range []flow.Element{start, a, end} {
		require.NoError(t, p.Add(e))
	}
	linkAll(t, [2]flow.Element{start, a}, [2]flow.Element{a, end})

	inst, rec := observeInstance(t, p)

	require.Equal(t, Completed, inst.State())
	for _, ph := range []observability.Phase{
		observability.PhaseEligible, observability.PhaseFolded,
		observability.PhaseDiscarded,
	} {
		require.Empty(t, compFacts(rec, ph), string(ph))
	}
}
