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
