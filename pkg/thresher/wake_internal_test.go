package thresher

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
)

// wakeEpoch anchors the controlled clock of the unit traces.
var wakeEpoch = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// TestHoldTimerWithoutService: without a running timer service (a volatile
// engine — no Repository) a hold is DECLINED, so the caller keeps the wait
// resident on its in-hub waiter rather than losing the timer (SRD-071 FR-3).
func TestHoldTimerWithoutService(t *testing.T) {
	th, err := New("engine-noservice",
		WithoutBanner(), WithoutStartupConfig())
	require.NoError(t, err)

	err = th.HoldTimer("i-1", "t-1", nil, wakeEpoch.Add(time.Hour), 0)
	require.Error(t, err, "a volatile engine holds nothing")

	// the paired withdraw is a no-op, never a panic.
	th.ReleaseTimer("i-1", "t-1")
}

// TestWakeSingleFlightLatch covers §4.6's latch directly: the first claim wins,
// a concurrent one is refused until released.
func TestWakeSingleFlightLatch(t *testing.T) {
	th, err := New("engine-latch", WithoutBanner(), WithoutStartupConfig())
	require.NoError(t, err)

	require.True(t, th.claimWake("i-1"), "the first wake claims")
	require.False(t, th.claimWake("i-1"),
		"a concurrent wake is refused — it delivers into the resident loop")
	require.True(t, th.claimWake("i-2"), "a different instance is unaffected")

	th.releaseWake("i-1")
	require.True(t, th.claimWake("i-1"), "the latch clears on release")
}

// TestWakeFailureIsLoud: a wake for an instance whose checkpoint is missing
// reports the failure instead of crashing the timer service (FR-4's
// per-instance, never-fatal contract).
func TestWakeFailureIsLoud(t *testing.T) {
	repo := memrepo.New()

	th, err := New("engine-wakefail",
		WithoutBanner(), WithoutStartupConfig(),
		WithRepository(repo),
		WithClock(clocktest.New(wakeEpoch)))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, th.Run(ctx))

	// no record for this id — the wake must degrade to a reported failure.
	th.hydrateFromTimer("ghost-instance", &instance.PendingTrigger{
		TrackID: "t-1",
	})

	require.Error(t, wakeErr("the checkpoint vanished before the wake", nil))
	require.Error(t, wakeErr("wrapped", context.Canceled))
}

// TestTimerServiceReleaseAndIdle covers the service's bookkeeping: an idle
// service holds no deadline, a hold becomes the nearest one, and a release
// withdraws it (idempotently).
func TestTimerServiceReleaseAndIdle(t *testing.T) {
	clk := clocktest.New(wakeEpoch)

	var woke []string

	ts := newTimerService(clk, func(id string, _ *instance.PendingTrigger) {
		woke = append(woke, id)
	})

	_, ok := ts.nearest()
	require.False(t, ok, "an idle service holds nothing")

	deadline := wakeEpoch.Add(time.Hour)
	ts.hold(timerHold{instanceID: "i-1", trackID: "t-1", deadline: deadline})

	got, ok := ts.nearest()
	require.True(t, ok)
	require.True(t, deadline.Equal(got))

	// an earlier hold wins the nearest slot.
	earlier := wakeEpoch.Add(30 * time.Minute)
	ts.hold(timerHold{instanceID: "i-2", trackID: "t-2", deadline: earlier})

	got, _ = ts.nearest()
	require.True(t, earlier.Equal(got))

	ts.release("i-2", "t-2")
	got, _ = ts.nearest()
	require.True(t, deadline.Equal(got), "the withdrawn hold is gone")

	ts.release("i-2", "t-2") // idempotent

	// nothing is due yet.
	ts.fireDue(context.Background())
	require.Empty(t, woke)

	// past the deadline it fires ONCE and drops the hold (overdue collapses).
	clk.Set(deadline.Add(time.Minute))
	ts.fireDue(context.Background())
	require.Equal(t, []string{"i-1"}, woke)

	ts.fireDue(context.Background())
	require.Equal(t, []string{"i-1"}, woke, "a fired hold is one-shot")
}

// TestTimerServiceRunStops: run returns when its context is canceled, and a
// canceled context stops fireDue mid-batch.
func TestTimerServiceRunStops(t *testing.T) {
	clk := clocktest.New(wakeEpoch)
	ts := newTimerService(clk, func(string, *instance.PendingTrigger) {})

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		ts.run(ctx)
		close(done)
	}()

	// a hold kicks the loop into recomputing its nearest deadline.
	ts.hold(timerHold{
		instanceID: "i-1", trackID: "t-1",
		deadline: wakeEpoch.Add(time.Hour),
	})

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not stop on context cancel")
	}

	// a canceled context aborts the fire batch without waking.
	var woke int

	ts2 := newTimerService(clk, func(string, *instance.PendingTrigger) {
		woke++
	})
	ts2.hold(timerHold{
		instanceID: "i-9", trackID: "t-9", deadline: wakeEpoch,
	})
	ts2.fireDue(ctx)
	require.Zero(t, woke, "a canceled wake batch fires nothing")
}
