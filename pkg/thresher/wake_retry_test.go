package thresher

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
)

// FIX-027 — a hold is a released instance's ONLY way back, so it must outlive a
// wake that failed. These are the regression tests for the ordering defect: the
// engine used to discard the hold before attempting the wake, stranding the
// instance in the store as in-flight with nothing left to revive it.

// unregisteredRecord seeds a checkpoint whose pinned process version this
// engine does not have registered — the realistic deployment-parity mismatch,
// and the cheapest way to make a wake fail for real rather than by injection.
func unregisteredRecord(t *testing.T, th *Thresher, instanceID string) {
	t.Helper()

	doc := &checkpoint.Document{
		InstanceID: instanceID,
		ProcessID:  "never-registered-proc",
		Version:    1,
		Status:     "Active",
	}

	payload, err := doc.Marshal()
	require.NoError(t, err)

	require.NoError(t, th.cfg.Repository().Save(context.Background(),
		repository.InstanceRecord{
			ID:      instanceID,
			Status:  repository.StatusActive,
			Payload: payload,
		}))
}

// retryEngine builds an armed engine on a controlled clock with an explicit
// wake-retry backoff.
func retryEngine(
	t *testing.T, name string, backoff time.Duration,
) (*Thresher, *clocktest.Clock, context.CancelFunc) {
	t.Helper()

	clk := clocktest.New(wakeEpoch)

	th, err := New(name,
		WithoutBanner(), WithoutStartupConfig(),
		WithRepository(memrepo.New()),
		WithClock(clk),
		WithWakeRetryBackoff(backoff))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, th.Run(ctx))

	return th, clk, cancel
}

// TestFailedWakeKeepsTheHold is the §1 probe inverted into an assertion: after
// a wake that fails, the deadline that was the instance's only way back is
// STILL ARMED, and the record is still in flight — recoverable, not stranded.
func TestFailedWakeKeepsTheHold(t *testing.T) {
	th, clk, cancel := retryEngine(t, "engine-keephold", time.Hour)
	defer cancel()

	const instID = "keephold-inst"

	unregisteredRecord(t, th, instID)

	require.NoError(t, th.HoldTimer(instID, "t-1", wakeTimerDef(t),
		wakeEpoch.Add(time.Hour), 0))

	_, armed := th.timerSvc.nearest()
	require.True(t, armed, "the hold is armed before the wake")

	// the deadline arrives; the wake fails (pinned version not registered).
	clk.Advance(2 * time.Hour)
	th.timerSvc.fireDue(context.Background())

	_, stillArmed := th.timerSvc.nearest()
	require.True(t, stillArmed,
		"a failed wake must NOT discard the instance's only way back")

	rec, ok, err := th.cfg.Repository().Load(context.Background(), instID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, repository.StatusActive, rec.Status,
		"the instance is still in flight — and now still reachable")
}

// TestFailedWakeBacksOff guards the spin the naive fix would cause: the
// deadline is ALREADY past when the wake fails, so a hold that is merely kept
// would be re-fired on every loop turn. The retry must wait out the backoff.
func TestFailedWakeBacksOff(t *testing.T) {
	const backoff = time.Hour

	th, clk, cancel := retryEngine(t, "engine-backoff", backoff)
	defer cancel()

	const instID = "backoff-inst"

	unregisteredRecord(t, th, instID)

	attempts := 0
	th.timerSvc.wake = func(string, *instance.PendingTrigger) bool {
		attempts++

		return false
	}

	require.NoError(t, th.HoldTimer(instID, "t-1", wakeTimerDef(t),
		wakeEpoch.Add(time.Minute), 0))

	clk.Advance(2 * time.Minute)

	// the clock stands still across these: a kept hold that had not been
	// deferred would be due every single time.
	for range 5 {
		th.timerSvc.fireDue(context.Background())
	}

	require.Equal(t, 1, attempts,
		"a failed wake is retried on a backoff, not once per loop turn")

	// past the backoff, it tries again.
	clk.Advance(backoff + time.Minute)
	th.timerSvc.fireDue(context.Background())

	require.Equal(t, 2, attempts, "the retry fires once the backoff elapses")
}

// TestFailedWakeRetriesAndSucceeds is the self-heal claim, and the reason no
// store-scanning sweeper is needed (FIX-027 §3.1.C): once the cause of the
// failure clears, the instance's own retained hold brings it back — no engine
// restart, no scan.
func TestFailedWakeRetriesAndSucceeds(t *testing.T) {
	const backoff = time.Hour

	th, clk, cancel := retryEngine(t, "engine-selfheal", backoff)
	defer cancel()

	const instID = "selfheal-inst"

	unregisteredRecord(t, th, instID)

	attempts := 0
	failing := true

	th.timerSvc.wake = func(string, *instance.PendingTrigger) bool {
		attempts++

		return !failing
	}

	require.NoError(t, th.HoldTimer(instID, "t-1", wakeTimerDef(t),
		wakeEpoch.Add(time.Minute), 0))

	clk.Advance(2 * time.Minute)
	th.timerSvc.fireDue(context.Background())

	require.Equal(t, 1, attempts)

	_, stillArmed := th.timerSvc.nearest()
	require.True(t, stillArmed, "kept for a retry")

	// the cause clears (the operator registers the missing version).
	failing = false

	clk.Advance(backoff + time.Minute)
	th.timerSvc.fireDue(context.Background())

	require.Equal(t, 2, attempts)

	_, held := th.timerSvc.nearest()
	require.False(t, held,
		"a SUCCESSFUL wake surrenders the hold — one-shot preserved")
}

// TestSuccessfulWakeStillWithdraws pins the healthy path: moving the withdraw
// below the fallible part must not regress M5's withdraw-the-losing-arms, so a
// hold that fires successfully is still gone afterwards.
func TestSuccessfulWakeStillWithdraws(t *testing.T) {
	th, clk, cancel := retryEngine(t, "engine-withdraws", time.Hour)
	defer cancel()

	woke := 0
	th.timerSvc.wake = func(string, *instance.PendingTrigger) bool {
		woke++

		return true
	}

	require.NoError(t, th.HoldTimer("ok-inst", "t-1", wakeTimerDef(t),
		wakeEpoch.Add(time.Minute), 0))

	clk.Advance(2 * time.Minute)
	th.timerSvc.fireDue(context.Background())

	require.Equal(t, 1, woke)

	_, held := th.timerSvc.nearest()
	require.False(t, held, "a fired one-shot timer does not fire twice")

	// and it stays gone — no resurrection on a later scan.
	clk.Advance(24 * time.Hour)
	th.timerSvc.fireDue(context.Background())

	require.Equal(t, 1, woke)
}

// TestFiringHoldIsNotReentered: while a wake is in progress the hold is marked
// firing, so a scan that overlaps a slow wake does not start a second one.
func TestFiringHoldIsNotReentered(t *testing.T) {
	th, clk, cancel := retryEngine(t, "engine-reenter", time.Hour)
	defer cancel()

	require.NoError(t, th.HoldTimer("busy-inst", "t-1", wakeTimerDef(t),
		wakeEpoch.Add(time.Minute), 0))

	clk.Advance(2 * time.Minute)

	// mark it firing exactly as fireDue's scan does, then scan again.
	th.timerSvc.mu.Lock()
	k := holdKey("busy-inst", "t-1")
	h := th.timerSvc.holds[k]
	h.firing = true
	th.timerSvc.holds[k] = h
	th.timerSvc.mu.Unlock()

	attempts := 0
	th.timerSvc.wake = func(string, *instance.PendingTrigger) bool {
		attempts++

		return true
	}

	th.timerSvc.fireDue(context.Background())

	require.Zero(t, attempts,
		"a hold already being woken is not woken a second time")
}

// TestWithWakeRetryBackoffValidates mirrors WithLeaseTTL's guard.
func TestWithWakeRetryBackoffValidates(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		_, err := New("engine-badbackoff",
			WithoutBanner(), WithoutStartupConfig(),
			WithWakeRetryBackoff(d))
		require.Error(t, err)
		require.Contains(t, err.Error(), "must be positive")
	}

	th, err := New("engine-goodbackoff",
		WithoutBanner(), WithoutStartupConfig(),
		WithWakeRetryBackoff(time.Minute))
	require.NoError(t, err)
	require.Equal(t, time.Minute, th.cfg.wakeBackoff)
}

// TestDefaultWakeRetryBackoff: the default is derived from the lease window, so
// a slower deployment does not get a retry cadence that churns against it.
func TestDefaultWakeRetryBackoff(t *testing.T) {
	th, err := New("engine-defbackoff",
		WithoutBanner(), WithoutStartupConfig())
	require.NoError(t, err)

	require.Equal(t, DefaultWakeRetryBackoff, th.cfg.wakeBackoff)
	require.Equal(t, DefaultLeaseTTL/2, th.cfg.wakeBackoff)
}

// TestFailedRebuildKeepsTheSubscriptionSet covers the OTHER half of the
// ordering defect (FIX-027 §2.2/§3.2.2): rebuildAndContinue used to withdraw
// the woken track's whole hold set BEFORE Restore/Run, so a rebuild failure
// took the subscriptions with it. For an Event-Based Gateway that is every
// racing arm at once.
//
// The failure must land BELOW the old withdraw to exercise it, so the record
// pins a REGISTERED version but names a node that version never had — Restore
// refuses it. (An unregistered version fails at the snapshot lookup, which sits
// above the withdraw even in the defective order, and would make this test
// vacuous — as the first draft of it was.)
func TestFailedRebuildKeepsTheSubscriptionSet(t *testing.T) {
	th, _, cancel := retryEngine(t, "engine-keepsubs", time.Hour)
	defer cancel()

	const (
		instID  = "keepsubs-inst"
		trackID = "t-gate"
	)

	p := shortTimerProcess(t, "keepsubs-proc")

	reg, err := th.RegisterProcess(p)
	require.NoError(t, err)

	doc := &checkpoint.Document{
		InstanceID: instID,
		ProcessID:  p.ID(),
		Version:    reg.Version(),
		Status:     "Active",
		Tracks: []checkpoint.TrackRecord{{
			ID:     trackID,
			State:  "TrackDehydrated",
			NodeID: "a-node-this-version-never-had",
		}},
	}

	payload, err := doc.Marshal()
	require.NoError(t, err)

	require.NoError(t, th.cfg.Repository().Save(context.Background(),
		repository.InstanceRecord{
			ID: instID, Status: repository.StatusActive, Payload: payload}))

	// the gateway's armed set: a timer arm and two subscription arms.
	require.NoError(t, th.HoldTimer(instID, trackID, wakeTimerDef(t),
		wakeEpoch.Add(time.Hour), 0))
	require.NoError(t, th.HoldSubscription(instID, trackID,
		wakeMessageDef(t, "cancel"), nil))
	require.NoError(t, th.HoldSubscription(instID, trackID,
		wakeSignalDef(t, "abort"), nil))

	th.subMu.Lock()
	before := len(th.subs)
	th.subMu.Unlock()
	require.Equal(t, 2, before)

	// the rebuild fails — past the point the old code had already withdrawn.
	err = th.rebuildAndContinue(instID, &instance.PendingTrigger{
		TrackID: trackID, EDef: wakeSignalDef(t, "abort")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "doesn't rebuild",
		"the failure must land below the withdraw to exercise the defect")

	th.subMu.Lock()
	after := len(th.subs)
	th.subMu.Unlock()

	require.Equal(t, before, after,
		"a failed rebuild must not withdraw the arms it never woke")

	_, armed := th.timerSvc.nearest()
	require.True(t, armed, "the timer arm survives too")
}
