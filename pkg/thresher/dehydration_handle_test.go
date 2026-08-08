package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// TestHandleSurvivesDehydration covers the residency contract of the public
// handle (SRD-071): an instance's IDENTITY outlives its object now, so a handle
// taken before a dehydration must keep speaking for the instance afterwards —
// and completion must mean COMPLETION.
//
// Both halves were broken when dehydration first landed: WaitCompletion
// returned "Dehydrated" with a nil error (a caller was told the process had
// finished when it had merely gone to sleep), and the handle answered
// "Dehydrated" forever while the real instance ran on and completed.
func TestHandleSurvivesDehydration(t *testing.T) {
	repo := memrepo.New()
	deadline := dehydrationEpoch.Add(2 * time.Hour)

	var hit atomic.Bool

	p := longTimerProc(t, "handle-dehy", deadline, &hit)

	th, fw, clk, cancel := bootDehydrationEngine(t, "engine-H", repo, p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 3*time.Second, 10*time.Millisecond)

	require.Equal(t, thresher.StateDehydrated, h.State(),
		"the released instance reports its residency through the handle")

	// Dehydration is NOT completion: the wait must not be satisfied by an
	// instance that merely released its goroutines.
	waitCtx, waitCancel := context.WithTimeout(
		context.Background(), 300*time.Millisecond)
	defer waitCancel()

	st, err := h.WaitCompletion(waitCtx)
	require.ErrorIs(t, err, context.DeadlineExceeded,
		"WaitCompletion must keep waiting across a dehydration")
	require.Equal(t, thresher.StateDehydrated, st)

	// wake it and let it finish.
	clk.Advance(3 * time.Hour)

	require.Eventually(t, hit.Load, 3*time.Second, 10*time.Millisecond)

	// the SAME handle now follows the rebuilt instance to its terminal state.
	doneCtx, doneCancel := context.WithTimeout(
		context.Background(), 3*time.Second)
	defer doneCancel()

	st, err = h.WaitCompletion(doneCtx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st,
		"the handle follows the instance across the wake to completion")
}

// TestStateDehydratedIsNamed: the state a released instance reports has a
// constant to compare against — it is part of the public vocabulary, not a
// bare string callers must guess.
func TestStateDehydratedIsNamed(t *testing.T) {
	require.Equal(t, thresher.InstanceState("Dehydrated"),
		thresher.StateDehydrated)
}

// TestCancelReachesADehydratedInstance is FIX-038 T-10. InstanceHandle.Cancel
// called inst.Cancel(), which cancels the instance's CONTEXT — but a dehydrated
// instance has no loop reading that context: its goroutines are gone and its
// state lives in a checkpoint. The request vanished, and the next wake rebuilt
// the instance and carried on as if it had never been made.
//
// The cancel now rides a rebuild, the way an incident operation does, and the
// fresh loop tears the instance down through stopAll BEFORE its park decision —
// stopAll, not a bare context cancel, because maybeDehydrate checks ls.stopping
// and a plain cancel would race the re-park and be lost again.
func TestCancelReachesADehydratedInstance(t *testing.T) {
	repo := memrepo.New()
	deadline := dehydrationEpoch.Add(2 * time.Hour)

	var hit atomic.Bool

	p := longTimerProc(t, "cancel-parked", deadline, &hit)

	th, fw, clk, cancel := bootDehydrationEngine(t, "engine-CP", repo, p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 3*time.Second, 10*time.Millisecond)

	require.Equal(t, thresher.StateDehydrated, h.State(),
		"the instance must be parked for this to test anything")

	ctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ccancel()

	st, err := h.Cancel(ctx)
	require.NoError(t, err, "cancelling a parked instance must reach it")
	require.Equal(t, thresher.StateTerminated, st,
		"the instance is terminated, not left parked")

	// And it stays cancelled: advancing past the timer must not resurrect it.
	clk.Advance(3 * time.Hour)

	require.Never(t, func() bool { return hit.Load() },
		500*time.Millisecond, 50*time.Millisecond,
		"a cancelled instance must not be woken by its own timer")
}

// TestCancelOnAParkedInstanceReportsAStoppedEngine is the other half of T-10:
// cancelling a DEHYDRATED instance needs a rebuild, and a rebuild needs a
// running engine. When the engine is gone the request cannot be delivered, and
// the caller must be told — the silent-loss failure this whole section exists
// to close would otherwise reappear as a nil error.
func TestCancelOnAParkedInstanceReportsAStoppedEngine(t *testing.T) {
	repo := memrepo.New()
	deadline := dehydrationEpoch.Add(2 * time.Hour)

	var hit atomic.Bool

	p := longTimerProc(t, "cancel-parked-stopped", deadline, &hit)

	th, fw, _, cancel := bootDehydrationEngine(t, "engine-CPS", repo, p)

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 3*time.Second, 10*time.Millisecond)

	require.Equal(t, thresher.StateDehydrated, h.State(),
		"the instance must be parked for this to test anything")

	cancel() // the engine goes away; nothing can rebuild the instance now

	ctx, ccancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer ccancel()

	_, err = h.Cancel(ctx)
	require.Error(t, err,
		"a cancel that cannot reach the instance must not report success")
}
