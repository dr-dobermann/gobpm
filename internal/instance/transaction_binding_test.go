package instance

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// TestBindTransaction is SRD-094 T-5's bind half: only a Transaction
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

// TestForeignBindingAbortsWithoutCompensating is SRD-094 T-5's invariant
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
}
