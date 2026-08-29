package thresher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/stretchr/testify/require"
)

// TestAwaitOpVerdictHonoursTheCallerContext: a request that rode a rebuild is
// answered by the fresh loop, but the caller's context bounds the wait — an
// operator must not block forever on a loop that died before it could answer.
func TestAwaitOpVerdictHonoursTheCallerContext(t *testing.T) {
	t.Run("the loop's verdict wins", func(t *testing.T) {
		resp := make(chan error, 1)
		resp <- errors.New("no such incident")

		require.ErrorContains(t,
			awaitOpVerdict(context.Background(), resp), "no such incident")
	})

	t.Run("a cancelled caller is released", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		require.ErrorIs(t, awaitOpVerdict(ctx, make(chan error)),
			context.Canceled, "the wait is bounded by the caller's context")
	})
}

// TestRebuildForOpReportsAFailedClaim: the latch is held and the engine goes
// away, so the request can never take it. It must report that rather than
// rebuilding beside the in-flight wake — two live loops over one instance is
// the defect FIX-037 §1.3 closed.
func TestRebuildForOpReportsAFailedClaim(t *testing.T) {
	th, stop := armedWakeEngine(t, "engine-op-claim-fail")

	proc := noneStartProcess(t, "p-op-claim-fail")
	_, err := th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	_, claimed := th.claimWake(h.ID())
	require.True(t, claimed, "a wake is in flight for this instance")

	stop() // the engine goes away while the latch is held

	resp := make(chan error, 1)

	err = th.rebuildForOp(context.Background(), h, "cancel", resp,
		instance.WithPendingCancel(resp))
	require.Error(t, err, "an unclaimable latch is reported, not ignored")
}

// TestRebuildForOpReportsAFailedRebuild: there is no checkpoint to rebuild the
// instance from. The failure must name the operation and the instance — an
// operator whose request never reached a loop has to be told.
//
// THE ABSENT RECORD IS PINNED, not assumed. `start → end` completes in
// microseconds and writes its record, and once that record exists the rebuild
// SUCCEEDS — so a test that merely starts the instance and asks immediately is
// racing its own fixture, and passes only while the request wins. It lost on a
// slower CI runner, where the record had already landed (2026-08-29,
// `docs/iteration-vocabulary`; the same commit passed on a re-run).
//
// Deleting the record states the premise as a fact instead: whatever the
// scheduler does, there is nothing to rebuild from.
func TestRebuildForOpReportsAFailedRebuild(t *testing.T) {
	th, stop := armedWakeEngine(t, "engine-op-rebuild-fail")
	defer stop()

	proc := noneStartProcess(t, "p-op-rebuild-fail")
	_, err := th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// wait for the record to exist, then take it away — waiting first so the
	// delete cannot land BEFORE the instance writes, which would leave the
	// same race running in the other direction.
	require.Eventually(t, func() bool {
		_, ok, lErr := th.cfg.Repository().Load(ctx, h.ID())

		return lErr == nil && ok
	}, 5*time.Second, 5*time.Millisecond,
		"the instance records itself; the test removes what it records")

	require.NoError(t, th.cfg.Repository().Delete(ctx, h.ID()))

	resp := make(chan error, 1)

	err = th.rebuildForOp(ctx, h, "cancel", resp,
		instance.WithPendingCancel(resp))
	require.Error(t, err)
	require.ErrorContains(t, err, "cancel: the parked instance")
	require.ErrorContains(t, err, h.ID())

	// the latch is released even on the failing path, so the instance is not
	// left permanently unwakeable.
	_, claimed := th.claimWake(h.ID())
	require.True(t, claimed, "a failed request releases the wake latch")

	th.releaseWake(h.ID())
}
