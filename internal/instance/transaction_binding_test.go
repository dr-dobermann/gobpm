package instance

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// TestBindTransaction is SRD-095 T-5's bind half: only a Transaction
// Sub-Process yields a binding, and a scope without one aborts as compensate.
func TestBindTransaction(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	task, err := activities.NewManualTask("t")
	require.NoError(t, err)
	require.Nil(t, bindTransaction(task), "a task opens no transaction")

	plain, err := activities.NewSubProcess("plain")
	require.NoError(t, err)
	require.Nil(t, bindTransaction(plain), "a plain sub-process is not bound")

	tx, err := activities.NewSubProcess("tx", activities.WithTransaction(
		activities.WithTransactionMethod("##Store"),
		activities.WithTransactionProtocol("wsat")))
	require.NoError(t, err)
	b := bindTransaction(tx)
	require.NotNil(t, b)
	require.Equal(t, activities.TransactionMethod("##Store"), b.Method())
	require.Equal(t, "wsat", b.Protocol())

	require.Equal(t, activities.TransactionCompensate,
		boundMethod(&scopeEntry{}), "no binding means the compensate default")
	require.Equal(t, activities.TransactionMethod("##Store"),
		boundMethod(&scopeEntry{tx: b}))
}

// TestForeignBindingAbortsWithoutCompensating is SRD-095 T-5's invariant
// half. Registration refuses a method no coordinator performs, so a scope
// bound to one can only be reached by building the instance directly — which
// this test does. The abort must then NOT compensate on the document's
// behalf: it reports a Failed compensation naming the method and tears the
// scope down, and control still leaves through the Cancel boundary.
func TestForeignBindingAbortsWithoutCompensating(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var reserved, cancelled atomic.Int32
	var seq, undoOrder atomic.Int64

	tx := buildCancelTx(t, &reserved, &seq, &undoOrder, true,
		activities.WithTransactionMethod("##Store"))

	cbEd, err := events.NewCancelEventDefinition()
	require.NoError(t, err)
	cb, err := events.NewBoundaryEvent("cancel-bnd", tx, cbEd, true)
	require.NoError(t, err)
	cancelledTask := hitTask(t, "cancelled", &cancelled, "", 0)

	p, err := process.New("foreign-binding")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	cxEnd, err := events.NewEndEvent("cx-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{start, tx, cb, cancelledTask, end, cxEnd} {
		require.NoError(t, p.Add(e))
	}
	linkAll(t,
		[2]flow.Element{start, tx},
		[2]flow.Element{tx, end},
		[2]flow.Element{cb, cancelledTask},
		[2]flow.Element{cancelledTask, cxEnd})

	inst, rec := observeInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.EqualValues(t, 1, reserved.Load(), "reserve ran")
	require.Zero(t, undoOrder.Load(),
		"nothing compensated on a foreign binding")
	require.EqualValues(t, 1, cancelled.Load(),
		"control still exited via the Cancel boundary")

	require.Empty(t, compFacts(rec, observability.PhaseThrown))
	failed := compFacts(rec, observability.PhaseFailed)
	require.Len(t, failed, 1)
	require.Equal(t, "##Store",
		failed[0].Details[observability.AttrTransactionMethod])
	require.Contains(t, failed[0].Details[observability.AttrError],
		"no coordinator")

	// The teardown is the ordinary one: the scope still reports Canceled.
	require.True(t,
		rec.phasesOf(observability.KindScope)[observability.PhaseCanceled],
		"the Transaction scope reported Canceled")
}

// gatedTxProcess builds start → book(Transaction) → end with the Cancel
// boundary exit, and inside book: s-start → reserve (guarded by undo) →
// hold (a gated conditional catch) → cancel. The gate parks the track at
// hold after reserve completed, which is the shape a checkpoint is taken in.
func gatedTxProcess(
	t *testing.T, key string, reserved, cancelled, undoRuns, gate *atomic.Int32,
	txOpts ...activities.TransactionOption,
) *snapshot.Snapshot {
	t.Helper()

	tx, err := activities.NewSubProcess("book",
		activities.WithTransaction(txOpts...))
	require.NoError(t, err)

	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	reserve := hitTask(t, "reserve", reserved, "", 0)
	undo := countCompHandler(t, "undoReserve", undoRuns)
	hold, err := events.NewIntermediateCatchEvent("hold", gatedCond(t, gate))
	require.NoError(t, err)

	cancEd, err := events.NewCancelEventDefinition()
	require.NoError(t, err)
	cancelEnd, err := events.NewEndEvent("cancel",
		events.WithCancelTrigger(cancEd))
	require.NoError(t, err)

	nodes := []flow.Element{sStart, reserve, hold, cancelEnd}
	nodes = append(nodes, guardWith(t, reserve, undo)...)
	for _, e := range nodes {
		require.NoError(t, tx.Add(e))
	}
	linkAll(t,
		[2]flow.Element{sStart, reserve},
		[2]flow.Element{reserve, hold},
		[2]flow.Element{hold, cancelEnd})

	cb, cancelledTask := addCancelBoundary(t, tx, cancelled)

	p, err := process.New(key)
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	cxEnd, err := events.NewEndEvent("cx-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{start, tx, cb, cancelledTask, end, cxEnd} {
		require.NoError(t, p.Add(e))
	}
	linkAll(t,
		[2]flow.Element{start, tx},
		[2]flow.Element{tx, end},
		[2]flow.Element{cb, cancelledTask},
		[2]flow.Element{cancelledTask, cxEnd})

	s, err := snapshot.New(p)
	require.NoError(t, err)

	return s
}

// atHold is the capture predicate: the Transaction scope is open (root plus
// one child) and the inner track is parked at hold.
func atHold(d *checkpoint.Document) bool {
	return len(d.Scopes) == 2 && len(d.Tracks) == 2
}

// TestWaitCheckpointCarriesPredecessorLedger is SRD-095 FR-8's regression:
// a checkpoint written at a wait right after a compensable activity carries
// that activity's ledger entry, so a restore from it still compensates on a
// Cancel. Before the fix the track declared the wait — and the loop
// checkpointed — before applying the evMoved that ledgers the predecessor,
// and the restored Transaction aborted without ever undoing reserve.
func TestWaitCheckpointCarriesPredecessorLedger(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var reserved, cancelled, undoRuns, gate atomic.Int32

	s := gatedTxProcess(t, "cr-ledger-at-wait",
		&reserved, &cancelled, &undoRuns, &gate)

	doc := captureAt(t, s, atHold)
	require.Len(t, doc.Ledgers, 1, "the wait checkpoint carries reserve")
	require.Equal(t, "reserve", doc.Ledgers[0].ActivityName)

	gate.Store(1)
	restored := restoreToDone(t, doc, s)

	require.Equal(t, Completed, restored.State())
	require.EqualValues(t, 1, undoRuns.Load(),
		"the restored abort compensates the reservation")
	require.EqualValues(t, 1, cancelled.Load())
}

// TestForeignBindingSurvivesRestore pins the restore-site bindings: a scope
// entry rebuilt from a checkpoint is bound from its node exactly as a fresh
// one is, so a Transaction whose method names no coordinator still aborts
// WITHOUT compensating after a restore — with a real ledger entry waiting,
// so the assertion is not vacuous. With the binding dropped on the restore
// path, boundMethod would fall back to compensate and the undo would run.
func TestForeignBindingSurvivesRestore(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var reserved, cancelled, undoRuns, gate atomic.Int32

	s := gatedTxProcess(t, "cr-foreign-binding",
		&reserved, &cancelled, &undoRuns, &gate,
		activities.WithTransactionMethod("##Store"))

	doc := captureAt(t, s, atHold)
	require.Len(t, doc.Ledgers, 1, "there is something to (not) compensate")

	gate.Store(1)

	restored, err := Restore(doc, s, scope.EmptyDataPath,
		cpRuntime(t), laxEP(t), nil, nil)
	require.NoError(t, err)

	rec := &obsRecorder{}
	restored.AddObserver(rec.record)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, restored.Run(ctx))

	select {
	case <-restored.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the restored instance did not finish")
	}

	require.Equal(t, Completed, restored.State())
	require.Zero(t, undoRuns.Load(),
		"a restored foreign binding must not compensate")
	require.EqualValues(t, 1, cancelled.Load(),
		"control left through the Cancel boundary after the restore")

	failed := compFacts(rec, observability.PhaseFailed)
	require.Len(t, failed, 1)
	require.Equal(t, "##Store",
		failed[0].Details[observability.AttrTransactionMethod])
}
