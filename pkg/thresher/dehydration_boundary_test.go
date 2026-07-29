package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// TestDehydratedBoundaryEscalates covers SRD-071 T-14 (FR-9a): an instance
// parked on a human task under an interrupting boundary timer DEHYDRATES, and
// the boundary's deadline wakes it and fires — the task is withdrawn and the
// escalation path runs.
//
// This is the v.1 probe inverted. Before M8/M9/M10 the same scenario released
// the instance and lost the escalation outright: the deadline passed, nothing
// fired, and the record sat Active with nothing left that could ever wake it.
// A silently missed business deadline is the worst failure this feature can
// produce, which is why the whole boundary slice exists.
func TestDehydratedBoundaryEscalates(t *testing.T) {
	repo := memrepo.New()
	dist := &annCollector{}
	clk := clocktest.New(dehydrationEpoch)

	var escalated atomic.Bool

	// 24h out: far past the dehydration threshold, so the instance releases.
	p := boundedTaskProc(t, "dehy-bnd",
		dehydrationEpoch.Add(24*time.Hour), &escalated)

	th, err := thresher.New("engine-bnd",
		thresher.WithoutBanner(),
		thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo),
		thresher.WithTaskDistributor(dist),
		thresher.WithClock(clk),
		thresher.WithLeaseTTL(time.Minute))
	require.NoError(t, err)

	fw := &factWatch{}
	sub := th.Observe(fw)

	t.Cleanup(sub.Cancel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err = th.RegisterProcess(p)
	require.NoError(t, err)
	require.NoError(t, th.Run(ctx))

	_, err = th.StartLatest(p.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool { return dist.count() == 1 },
		3*time.Second, 10*time.Millisecond,
		"the guarded task must be announced")

	// BOTH waits are held — the task by the distributor, the boundary by the
	// timer service — so the instance may release. Before M10 the armed
	// boundary kept it resident (M8's guard); before M8 it released and lost
	// the escalation.
	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 3*time.Second, 10*time.Millisecond,
		"an instance whose task AND boundary are both held must dehydrate")

	// nothing is running now: moving the clock past the escalation deadline is
	// the only thing that can bring it back.
	clk.Advance(25 * time.Hour)

	require.Eventually(t, func() bool { return escalated.Load() },
		5*time.Second, 10*time.Millisecond,
		"the boundary deadline must wake the instance and escalate")

	require.True(t, fw.saw(observability.KindInstanceState,
		observability.PhaseHydrated),
		"the escalation is an observable wake")
}
