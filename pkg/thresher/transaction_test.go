package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// SRD-061 — the Transaction Sub-Process abort end-to-end through the public
// engine: a Cancel End Event inside a Transaction compensates the completed
// activities (reverse completion order) and hands control out through the
// Cancel boundary onto the "cancelled" path (ADR-028 §2.3).

// TestTransactionCancelE2E: a booking Transaction reserves a seat and charges a
// card (both compensable), then a Cancel End aborts it — the charge is refunded
// and the seat released, and control leaves via the Cancel boundary to notify
// the customer. The observer sees the Transaction scope reported Canceled.
func TestTransactionCancelE2E(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var reserved, charged, released, refunded, notified atomic.Bool

	// the Transaction: start → reserve → charge → cancel-booking (Cancel End);
	// reserve and charge each guarded by a Compensation boundary.
	tx, err := activities.NewSubProcess("booking", activities.WithTransaction())
	require.NoError(t, err)

	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	reserve := laneTask(t, "reserve", &reserved)
	charge := laneTask(t, "charge", &charged)

	cancEd, err := events.NewCancelEventDefinition()
	require.NoError(t, err)
	cancelEnd, err := events.NewEndEvent("cancel-booking",
		events.WithCancelTrigger(cancEd))
	require.NoError(t, err)

	nodes := []flow.Element{sStart, reserve, charge, cancelEnd}
	nodes = append(nodes, compGuardE2E(t, reserve, "release-seat", &released)...)
	nodes = append(nodes, compGuardE2E(t, charge, "refund-card", &refunded)...)
	for _, e := range nodes {
		require.NoError(t, tx.Add(e))
	}
	link(t, sStart, reserve)
	link(t, reserve, charge)
	link(t, charge, cancelEnd)

	// the Cancel boundary on the Transaction → notify-customer.
	cbEd, err := events.NewCancelEventDefinition()
	require.NoError(t, err)
	cb, err := events.NewBoundaryEvent("cancel-bnd", tx, cbEd, true)
	require.NoError(t, err)
	notify := laneTask(t, "notify-customer", &notified)

	proc, err := process.New("transaction-cancel-e2e")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	cxEnd, err := events.NewEndEvent("cx-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, tx, cb, notify, end, cxEnd} {
		require.NoError(t, proc.Add(e))
	}
	link(t, start, tx)
	link(t, tx, end)
	link(t, cb, notify)
	link(t, notify, cxEnd)

	th, err := thresher.New("tx-cancel-e2e-engine")
	require.NoError(t, err)

	c := &collector{}
	sub := th.Observe(c)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, th.Run(ctx))
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	defer wcancel()
	state, werr := h.WaitCompletion(wctx)
	require.NoError(t, werr)
	require.Equal(t, thresher.StateCompleted, state)

	require.NoError(t, th.Shutdown(context.Background()))
	sub.Cancel() // drains buffered facts

	require.True(t, reserved.Load(), "reserve ran")
	require.True(t, charged.Load(), "charge ran")
	require.True(t, released.Load(), "the seat reservation was compensated")
	require.True(t, refunded.Load(), "the card charge was compensated")
	require.True(t, notified.Load(), "control exited via the Cancel boundary")

	require.True(t,
		c.sawKindPhase(observability.KindScope, observability.PhaseCanceled),
		"the Transaction scope reported Canceled")
}
