package instance

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// SRD-061 M2 — the Transaction Sub-Process abort: a Cancel End Event inside a
// Transaction compensates the completed activities, terminates the residuals,
// and hands control out through the Cancel boundary (ADR-028 §2.3).

// buildCancelTx builds a Transaction Sub-Process "book": start → reserve →
// cancel-end. When compensable, a Compensation boundary+handler guards reserve
// (recording undoOrder when it runs), so the completed reservation is on the
// ledger the abort must sweep. Returns the Transaction, ready to Add to an outer
// process.
func buildCancelTx(
	t *testing.T, reserved *atomic.Int32, seq, undoOrder *atomic.Int64,
	compensable bool,
) *activities.SubProcess {
	t.Helper()

	tx, err := activities.NewSubProcess("book", activities.WithTransaction())
	require.NoError(t, err)

	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	reserve := hitTask(t, "reserve", reserved, "", 0)

	cancEd, err := events.NewCancelEventDefinition()
	require.NoError(t, err)
	cancelEnd, err := events.NewEndEvent("cancel",
		events.WithCancelTrigger(cancEd))
	require.NoError(t, err)

	nodes := []flow.Element{sStart, reserve, cancelEnd}
	if compensable {
		undo := readingHandler(t, "undoReserve", "", nil, seq, undoOrder)
		nodes = append(nodes, guardWith(t, reserve, undo)...)
	}

	for _, e := range nodes {
		require.NoError(t, tx.Add(e))
	}
	linkAll(t,
		[2]flow.Element{sStart, reserve},
		[2]flow.Element{reserve, cancelEnd})

	return tx
}

// addCancelBoundary attaches an interrupting Cancel boundary on tx routing to a
// fresh "cancelled" task, and returns the boundary + task to Add to the outer
// process.
func addCancelBoundary(
	t *testing.T, tx *activities.SubProcess, cancelled *atomic.Int32,
) (*events.BoundaryEvent, *activities.ServiceTask) {
	t.Helper()

	cbEd, err := events.NewCancelEventDefinition()
	require.NoError(t, err)
	cb, err := events.NewBoundaryEvent("cancel-bnd", tx, cbEd, true)
	require.NoError(t, err)

	return cb, hitTask(t, "cancelled", cancelled, "", 0)
}

// TestTransactionCancelAbort — the full abort: reserve completes, the Cancel End
// aborts the Transaction, the reservation is compensated, and control exits the
// Cancel boundary onto the "cancelled" path (ADR-028 §2.3, SRD-061 FR-5).
func TestTransactionCancelAbort(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var reserved, cancelled atomic.Int32
	var seq, undoOrder atomic.Int64

	tx := buildCancelTx(t, &reserved, &seq, &undoOrder, true)
	cb, cancelledTask := addCancelBoundary(t, tx, &cancelled)

	p, err := process.New("booking-cancel")
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
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, reserved.Load(), "reserve ran")
	require.EqualValues(t, 1, undoOrder.Load(),
		"the reservation was compensated before the abort finished")
	require.EqualValues(t, 1, cancelled.Load(),
		"control exited via the Cancel boundary")

	comps := compFacts(rec, observability.PhaseCompensating)
	require.Len(t, comps, 1)
	require.Equal(t, "reserve", comps[0].NodeName)

	// FR-7 (Option A): the Transaction scope reports Canceled at teardown.
	require.True(t, rec.phasesOf(observability.KindScope)[observability.PhaseCanceled],
		"the Transaction scope reported Canceled")
}

// TestTransactionCancelNoBoundary — a Transaction with a Cancel End but no Cancel
// boundary: the abort still compensates and tears the host down, and no token
// continues (ADR-028 §2.4). The instance completes with the reservation undone.
func TestTransactionCancelNoBoundary(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var reserved atomic.Int32
	var seq, undoOrder atomic.Int64

	tx := buildCancelTx(t, &reserved, &seq, &undoOrder, true)

	p, err := process.New("booking-cancel-no-boundary")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, tx, end} {
		require.NoError(t, p.Add(e))
	}
	linkAll(t, [2]flow.Element{start, tx}, [2]flow.Element{tx, end})

	inst := runInstance(t, p)

	require.Equal(t, Completed, inst.State(),
		"the abort tears the host down; with no token left the instance settles")
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, reserved.Load(), "reserve ran")
	require.EqualValues(t, 1, undoOrder.Load(), "the reservation was compensated")
}

// TestTransactionCancelNoCompensation — a Transaction whose activity is not
// compensable (empty ledger): the abort short-circuits the sweep and finalizes
// at once, still exiting via the Cancel boundary (cancelTransaction's empty-queue
// branch → finishSweep → finalizeTransaction).
func TestTransactionCancelNoCompensation(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var reserved, cancelled atomic.Int32
	var seq, undoOrder atomic.Int64

	tx := buildCancelTx(t, &reserved, &seq, &undoOrder, false)
	cb, cancelledTask := addCancelBoundary(t, tx, &cancelled)

	p, err := process.New("booking-cancel-no-comp")
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

	inst := runInstance(t, p)

	require.Equal(t, Completed, inst.State())
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, reserved.Load(), "reserve ran")
	require.EqualValues(t, 0, undoOrder.Load(), "nothing to compensate")
	require.EqualValues(t, 1, cancelled.Load(),
		"control still exited via the Cancel boundary")
}

// TestTransactionAbortGuards covers the defensive early-returns: a Cancel whose
// scope is already closed (cancelTransaction / finalizeTransaction) and a Cancel
// boundary lookup on a node that carries no boundaries (cancelBoundaryOn) are all
// benign no-ops — the loop is single-writer, so these are unreachable in flight
// but guard against a late or malformed signal.
func TestTransactionAbortGuards(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	inst, ls := openInstance(t)

	// a closed/unknown Transaction scope: both entry points return silently.
	ls.cancelTransaction(t.Context(), &track{scopePath: scope.DataPath("gone")})
	ls.finalizeTransaction(t.Context(),
		&compSweep{path: scope.DataPath("gone"), txHost: &track{}})

	// a non-boundary-hosting node has no Cancel boundary.
	se, err := events.NewStartEvent("s")
	require.NoError(t, err)
	require.Nil(t, cancelBoundaryOn(se))

	// decScope on an aborting scope drains silently: the residual counts out but
	// the scope is NOT completed normally — finalizeTransaction owns the teardown
	// (SRD-061 FR-5). Covered directly because the integration race between the
	// aborting track's drain and the sweep's finalize is non-deterministic.
	txPath := scope.DataPath("tx")
	ls.scopes[txPath] = &scopeEntry{active: 1, aborting: true, host: &track{}}
	ls.decScope(t.Context(), &track{scopePath: txPath})
	require.Contains(t, ls.scopes, txPath,
		"an aborting scope is not completed by decScope")
	require.Zero(t, ls.scopes[txPath].active)

	require.NoError(t, inst.LastErr())
}
