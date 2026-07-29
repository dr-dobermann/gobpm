package instance

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// TestCancelledWaitReleasesItsHolds covers SRD-071 T-13 (FR-3b): a track
// CANCELLED by an interrupting boundary releases the holds its wait
// registered.
//
// The wait never fired, so the delivery path that normally withdraws is never
// reached, and the instance keeps running, so the teardown path is not reached
// either — before M8 those were the only two withdrawal sites and the hold
// simply survived its own wait. A deadline or subscription outliving the wait
// it belongs to is not inert: it stays armed against a track that no longer
// exists and can wake a later dehydration cycle for it.
func TestCancelledWaitReleasesItsHolds(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// a sub-process parked forever on a message receive, guarded by an
	// interrupting signal boundary — the receive is held, then canceled.
	sp, err := activities.NewSubProcess("guarded")
	require.NoError(t, err)

	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	stuck := blockedReceive(t, "stuck")
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{sStart, stuck, sEnd} {
		require.NoError(t, sp.Add(e))
	}

	linkAll(t, [2]flow.Element{sStart, stuck}, [2]flow.Element{stuck, sEnd})

	sig, err := events.NewSignal("break",
		data.MustItemDefinition(values.NewVariable(1)))
	require.NoError(t, err)
	sdef, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	bnd, err := events.NewBoundaryEvent("bnd", sp, sdef, true)
	require.NoError(t, err)

	var exc atomic.Int32

	excTask := hitTask(t, "exc", &exc, "", 0)

	p, err := process.New("withdraw-on-cancel")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	excEnd, err := events.NewEndEvent("exc-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, sp, bnd, excTask, end, excEnd} {
		require.NoError(t, p.Add(e))
	}

	linkAll(t,
		[2]flow.Element{start, sp},
		[2]flow.Element{sp, end},
		[2]flow.Element{bnd, excTask},
		[2]flow.Element{excTask, excEnd})

	s, err := snapshot.New(p)
	require.NoError(t, err)

	holders := newFakeHolders()
	cp := &capturingProducer{procs: map[string]eventproc.EventProcessor{}}

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), cp, nil,
		WithWaitHolders(holders))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	// the inner receive parks and its subscription is HELD.
	require.Eventually(t, func() bool { return holders.subCount() == 1 },
		3*time.Second, 5*time.Millisecond,
		"the parked receive must hand its subscription to a holder")

	var proc eventproc.EventProcessor

	require.Eventually(t, func() bool {
		proc = cp.watch()

		return proc != nil
	}, 3*time.Second, 5*time.Millisecond)

	// fire the boundary: the sub-process is canceled with its inner track
	// still parked on the held receive.
	require.NoError(t, proc.ProcessEvent(ctx, proc.(*boundaryWatch).def))

	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		3*time.Second, 5*time.Millisecond)
	require.NoError(t, inst.LastErr())
	require.EqualValues(t, 1, exc.Load(), "the exception flow must run")

	// THE ASSERTION: the canceled wait's hold is gone. Before M8 this map
	// still held the subscription of a track that no longer exists.
	require.Zero(t, holders.subCount(),
		"a canceled wait must not leave its subscription held")
}
