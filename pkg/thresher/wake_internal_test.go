package thresher

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	gerrs "github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
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

	err = th.HoldTimer("i-1", "t-1", nil, wakeEpoch.Add(time.Hour), 0, exec.WaitNode)
	require.Error(t, err, "a volatile engine holds nothing")

	// a subscription hold is declined for the same reason.
	require.Error(t, th.HoldSubscription("i-1", "t-1", nil, nil, exec.WaitNode))

	// the paired withdraw is a no-op, never a panic.
	th.ReleaseWaits("i-1", "t-1")
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

	ts := newTimerService(clk, DefaultWakeRetryBackoff,
		func(id string, _ *instance.PendingTrigger) bool {
			woke = append(woke, id)

			return true
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
	ts := newTimerService(clk, DefaultWakeRetryBackoff,
		func(string, *instance.PendingTrigger) bool { return true })

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

	ts2 := newTimerService(clk, DefaultWakeRetryBackoff,
		func(string, *instance.PendingTrigger) bool {
			woke++

			return true
		})
	ts2.hold(timerHold{
		instanceID: "i-9", trackID: "t-9", deadline: wakeEpoch,
	})
	ts2.fireDue(ctx)
	require.Zero(t, woke, "a canceled wake batch fires nothing")
}

// wakeSignalDef / wakeMessageDef build REAL definitions — the hub's waiters
// resolve them by concrete type, so a stub would never register.
func wakeSignalDef(t *testing.T, name string) flow.EventDefinition {
	t.Helper()

	sig, err := events.NewSignal(name, nil)
	require.NoError(t, err)

	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	return def
}

func wakeMessageDef(t *testing.T, name string) flow.EventDefinition {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	return events.MustMessageEventDefinition(
		bpmncommon.MustMessage(name, data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID(name+"_in"))), nil)
}

func wakeTimerDef(t *testing.T) flow.EventDefinition {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	expr, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(time.Time{})),
		func(context.Context, data.Source) (data.Value, error) {
			return values.NewVariable(wakeEpoch.Add(time.Hour)), nil
		})
	require.NoError(t, err)

	def, err := events.NewTimerEventDefinition(expr, nil, nil)
	require.NoError(t, err)

	return def
}

// armedWakeEngine boots a checkpoint-armed, started engine for the holder
// bookkeeping traces.
func armedWakeEngine(t *testing.T, name string) (*Thresher, context.CancelFunc) {
	t.Helper()

	th, err := New(name,
		WithoutBanner(), WithoutStartupConfig(),
		WithRepository(memrepo.New()),
		WithClock(clocktest.New(wakeEpoch)))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, th.Run(ctx))

	return th, cancel
}

// TestHoldAndReleaseWaits covers the per-track hold bookkeeping (SRD-071
// FR-3/FR-7): a track may hold SEVERAL subscriptions (the Event-Based Gateway
// set), and ReleaseWaits withdraws them all at once — plus its timer — and is
// idempotent.
func TestHoldAndReleaseWaits(t *testing.T) {
	th, cancel := armedWakeEngine(t, "engine-holds")
	defer cancel()

	sigA, sigB := wakeSignalDef(t, "sig-a"), wakeSignalDef(t, "sig-b")

	require.NoError(t, th.HoldSubscription("i-1", "t-1", sigA, nil, exec.WaitNode))
	require.NoError(t, th.HoldSubscription("i-1", "t-1", sigB, nil, exec.WaitNode))
	require.NoError(t, th.HoldSubscription("i-1", "t-2", sigA, []string{"K"}, exec.WaitNode))

	require.NoError(t, th.HoldTimer("i-1", "t-1",
		wakeTimerDef(t), wakeEpoch.Add(time.Hour), 0, exec.WaitNode))

	th.subMu.Lock()
	require.Len(t, th.subs, 3, "each armed wait holds its own subscription")
	th.subMu.Unlock()

	// the winning arm's release withdraws the whole set for THAT track only.
	th.ReleaseWaits("i-1", "t-1")

	th.subMu.Lock()
	require.Len(t, th.subs, 1, "only the other track's hold survives")
	th.subMu.Unlock()

	_, held := th.timerSvc.nearest()
	require.False(t, held, "the track's timer hold went with it")

	th.ReleaseWaits("i-1", "t-1") // idempotent
	th.ReleaseWaits("i-1", "t-2")

	th.subMu.Lock()
	require.Empty(t, th.subs)
	th.subMu.Unlock()
}

// TestSubHolderCorrelationKeys: the holder stands in for the instance, so it
// contributes the same conversation keys the message waiter filters on.
func TestSubHolderCorrelationKeys(t *testing.T) {
	th, cancel := armedWakeEngine(t, "engine-keys")
	defer cancel()

	eDef := wakeMessageDef(t, "payment")
	require.NoError(t,
		th.HoldSubscription("i-1", "t-1", eDef, []string{"ORD-1"}, exec.WaitNode))

	th.subMu.Lock()
	h := th.subs[subKey{"i-1", "t-1", eDef.ID()}]
	th.subMu.Unlock()

	require.NotNil(t, h)
	require.Equal(t, []string{"ORD-1"}, h.CorrelationKeys(),
		"the holder subscribes to the instance's conversation")
	require.Equal(t, "i-1", h.instanceID)
}

// TestWakeUnknownInstanceIsLoud: a trigger for an instance with no checkpoint
// reports a failure rather than crashing the holder.
func TestWakeUnknownInstanceIsLoud(t *testing.T) {
	th, cancel := armedWakeEngine(t, "engine-unknown")
	defer cancel()

	eDef := wakeSignalDef(t, "ghost-sig")
	require.NoError(t, th.HoldSubscription("ghost", "t-1", eDef, nil, exec.WaitNode))

	th.subMu.Lock()
	h := th.subs[subKey{"ghost", "t-1", eDef.ID()}]
	th.subMu.Unlock()

	// drives wakeFromSubscription → wakeInstance → the missing-record path.
	require.NoError(t, h.ProcessEvent(context.Background(), eDef))

	err := th.wakeInstance("ghost", &instance.PendingTrigger{
		TrackID: "t-1", EDef: eDef})
	require.Error(t, err, "a wake with no checkpoint is an error, not a panic")
}

// TestWakeSingleFlightRefusesSecond: with a wake already latched, a second
// trigger for the same instance is a no-op (it will ride the resident loop).
func TestWakeSingleFlightRefusesSecond(t *testing.T) {
	th, cancel := armedWakeEngine(t, "engine-latched")
	defer cancel()

	require.True(t, th.claimWake("i-1"))

	err := th.wakeInstance("i-1", &instance.PendingTrigger{
		TrackID: "t-1", EDef: wakeSignalDef(t, "latched-sig")})
	require.NoError(t, err,
		"a second concurrent trigger defers to the in-flight wake")

	th.releaseWake("i-1")
}

// TestClaimForWakeExhausts: a repository whose CAS never succeeds exhausts the
// bounded retry and reports, instead of silently swallowing the trigger.
func TestClaimForWakeExhausts(t *testing.T) {
	th, cancel := armedWakeEngine(t, "engine-cas")
	defer cancel()

	repo := th.cfg.Repository()
	ctx := context.Background()

	require.NoError(t, repo.Save(ctx, repository.InstanceRecord{
		ID: "i-cas", Status: repository.StatusActive}))

	// a foreign write between every load and save keeps the CAS losing.
	th.cfg.repository = &casLoser{Repository: repo}

	_, err := th.claimForWake(ctx, "i-cas")
	require.Error(t, err)
	require.Contains(t, err.Error(), "claim the record")
}

// casLoser fails every Save, standing in for a record that keeps moving.
type casLoser struct {
	repository.Repository
}

func (c *casLoser) Save(context.Context, repository.InstanceRecord) error {
	return errNoHold("CAS conflict")
}

// TestRebuildAndContinueFailures covers the rebuild's loud degradations
// (SRD-071 FR-4): a checkpoint that does not decode, and one pinning a process
// version this engine has not registered. Each reports rather than crashing the
// holder that fired.
func TestRebuildAndContinueFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("a checkpoint that doesn't decode", func(t *testing.T) {
		th, cancel := armedWakeEngine(t, "engine-badpayload")
		defer cancel()

		require.NoError(t, th.cfg.Repository().Save(ctx,
			repository.InstanceRecord{
				ID:      "bad-doc",
				Status:  repository.StatusActive,
				Payload: []byte("not json"),
			}))

		err := th.rebuildAndContinue("bad-doc", &instance.PendingTrigger{
			TrackID: "t-1", EDef: wakeSignalDef(t, "s")})
		require.Error(t, err)
		require.Contains(t, err.Error(), "doesn't decode")
	})

	t.Run("an unregistered pinned version", func(t *testing.T) {
		th, cancel := armedWakeEngine(t, "engine-noversion")
		defer cancel()

		doc := &checkpoint.Document{
			InstanceID: "ghost-proc",
			ProcessID:  "never-registered",
			Version:    1,
			Status:     "Active",
		}

		payload, err := doc.Marshal()
		require.NoError(t, err)

		require.NoError(t, th.cfg.Repository().Save(ctx,
			repository.InstanceRecord{
				ID:      "ghost-proc",
				Status:  repository.StatusActive,
				Payload: payload,
			}))

		err = th.rebuildAndContinue("ghost-proc", &instance.PendingTrigger{
			TrackID: "t-1", EDef: wakeSignalDef(t, "s")})
		require.Error(t, err)
		require.Contains(t, err.Error(), "isn't registered")
	})

	t.Run("a vanished record", func(t *testing.T) {
		th, cancel := armedWakeEngine(t, "engine-vanished")
		defer cancel()

		err := th.rebuildAndContinue("no-such-record",
			&instance.PendingTrigger{
				TrackID: "t-1", EDef: wakeSignalDef(t, "s")})
		require.Error(t, err)
		require.Contains(t, err.Error(), "vanished")
	})
}

// TestReportWakeFailureIsObservable: a failed wake surfaces as an operator
// fact, never a silent drop.
func TestReportWakeFailureIsObservable(t *testing.T) {
	th, cancel := armedWakeEngine(t, "engine-report")
	defer cancel()

	sink := &wakeFactSink{}
	sub := th.Observe(sink)

	defer sub.Cancel()

	// the timer-service callback funnels a failing wake into the report.
	th.hydrateFromTimer("absent", &instance.PendingTrigger{
		TrackID: "t-1", EDef: wakeSignalDef(t, "s")})

	require.Eventually(t, func() bool {
		return sink.sawFailed()
	}, 2*time.Second, 10*time.Millisecond,
		"a failed wake must be operator-visible")
}

// wakeFactSink collects instance-state failure facts.
type wakeFactSink struct {
	mu    sync.Mutex
	facts []observability.Fact
}

func (w *wakeFactSink) OnFact(f observability.Fact) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.facts = append(w.facts, f)
}

func (w *wakeFactSink) sawFailed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, f := range w.facts {
		if f.Kind == observability.KindInstanceState &&
			f.Phase == observability.PhaseFailed &&
			f.Details["reason"] == "wake" {
			return true
		}
	}

	return false
}

// TestWakeResidentInstance covers wakeInstance's RESIDENT fork (SRD-071 FR-3):
// an instance that still holds its loop takes delivery into it rather than
// being rebuilt — the holder reaches the live loop exactly as an in-hub waiter
// would. A trigger whose wait has already moved on is benignly dropped there.
func TestWakeResidentInstance(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	th, cancel := armedWakeEngine(t, "engine-resident")
	defer cancel()

	// a SHORT timer stays resident (sub-threshold — never dehydrates).
	p := shortTimerProcess(t, "wake-resident")

	_, err := th.RegisterProcess(p)
	require.NoError(t, err)

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		inst, err := th.instanceByID(h.ID())

		return err == nil && inst.State() == instance.Active
	}, 2*time.Second, 5*time.Millisecond, "the instance must be running")

	// the resident branch: delivered into the live loop, never a rebuild.
	require.NoError(t, th.wakeInstance(h.ID(), &instance.PendingTrigger{
		TrackID: "a-wait-that-moved-on",
		EDef:    wakeSignalDef(t, "resident-sig"),
	}))
}

// shortTimerProcess builds start → short timer → end: it parks but stays
// resident (the deadline is under the dehydration threshold).
func shortTimerProcess(t *testing.T, key string) *process.Process {
	t.Helper()

	p, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	expr, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(time.Time{})),
		func(context.Context, data.Source) (data.Value, error) {
			return values.NewVariable(wakeEpoch.Add(time.Minute)), nil
		})
	require.NoError(t, err)

	def, err := events.NewTimerEventDefinition(expr, nil, nil)
	require.NoError(t, err)

	wait, err := events.NewIntermediateCatchEvent("wait", def,
		foundation.WithID(key+"-wait"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, wait, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, wait)
	require.NoError(t, err)
	_, err = flow.Link(wait, end)
	require.NoError(t, err)

	return p
}

// TestWakeWithdrawsTheTracksHolds covers SRD-071 FR-3a's withdraw-siblings
// step at the level where it is actually observable — the engine's holder
// registry. A track holding a SET (an Event-Based Gateway's arms: a deadline
// plus a subscription) must have ALL of them withdrawn when its wait is woken,
// keyed to the DEHYDRATED track's id — the continuation fork replacing it is a
// fresh track, so nothing else would ever release them. A leaked hold outlives
// the wait it stood for and wakes an instance that has long moved on.
func TestWakeWithdrawsTheTracksHolds(t *testing.T) {
	th, cancel := armedWakeEngine(t, "engine-withdraw")
	defer cancel()

	const (
		instID  = "i-ebg"
		trackID = "t-gate"
	)

	// the gateway's armed set: a timer arm and a message arm.
	require.NoError(t, th.HoldTimer(instID, trackID,
		wakeTimerDef(t), wakeEpoch.Add(4*time.Hour), 0, exec.WaitNode))
	require.NoError(t, th.HoldSubscription(instID, trackID,
		wakeMessageDef(t, "cancel"), nil, exec.WaitNode))

	require.Equal(t, 1, len(th.subs), "the message arm is held")

	_, armed := th.timerSvc.nearest()
	require.True(t, armed, "the timer arm is held")

	// waking the track ends its wait — the whole set goes, winners and losers.
	th.ReleaseWaits(instID, trackID)

	th.subMu.Lock()
	require.Empty(t, th.subs,
		"the losing message arm's subscription is withdrawn")
	th.subMu.Unlock()

	_, stillArmed := th.timerSvc.nearest()
	require.False(t, stillArmed,
		"the losing timer arm's deadline is withdrawn")
}

// TestHoldSubscriptionGuards covers the decline paths (SRD-071 FR-3): a nil
// definition and a volatile engine both refuse the hold, so the caller keeps
// the wait resident rather than stranding it.
func TestHoldSubscriptionGuards(t *testing.T) {
	t.Run("a nil definition", func(t *testing.T) {
		th, cancel := armedWakeEngine(t, "engine-nildef")
		defer cancel()

		require.Error(t, th.HoldSubscription("i-1", "t-1", nil, nil, exec.WaitNode))
	})

	t.Run("a volatile engine holds nothing", func(t *testing.T) {
		th, err := New("engine-volatile",
			WithoutBanner(), WithoutStartupConfig())
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, th.Run(ctx))

		err = th.HoldSubscription("i-1", "t-1",
			wakeSignalDef(t, "vol-sig"), nil, exec.WaitNode)
		require.Error(t, err, "no checkpoint to wake from — decline")
		require.Contains(t, err.Error(), "no checkpoints")
	})

	t.Run("an unregisterable hold releases quietly", func(t *testing.T) {
		th, cancel := armedWakeEngine(t, "engine-unreg")
		defer cancel()

		require.NoError(t, th.HoldSubscription("i-1", "t-1",
			wakeSignalDef(t, "unreg-sig"), nil, exec.WaitNode))

		// tearing the hub down first makes the withdraw fail — ReleaseWaits
		// must absorb it (the hold is gone from the registry regardless).
		require.NoError(t, th.Shutdown(context.Background()))

		th.ReleaseWaits("i-1", "t-1")

		th.subMu.Lock()
		require.Empty(t, th.subs)
		th.subMu.Unlock()
	})
}

// TestWakeFromSubscriptionDropsAForeignConversation covers the benign-drop
// branch (ADR-016): a wake refused because the trigger belongs to another
// conversation is logged and dropped, NOT reported as an engine failure.
func TestWakeFromSubscriptionDropsAForeignConversation(t *testing.T) {
	th, cancel := armedWakeEngine(t, "engine-drop")
	defer cancel()

	sink := &wakeFactSink{}
	sub := th.Observe(sink)

	defer sub.Cancel()

	h := &subHolder{instanceID: "i-drop", trackID: "t-1", th: th}

	// a correlation-classed failure must not surface as a wake failure.
	th.reportDropOrFailure(h, wakeSignalDef(t, "drop-sig"),
		gerrs.New(
			gerrs.M("the trigger belongs to another conversation"),
			gerrs.C(correlationDropClass)))

	require.Never(t, sink.sawFailed, 200*time.Millisecond, 20*time.Millisecond,
		"a foreign conversation is a benign drop, not a failure")

	// anything else still reports.
	th.reportDropOrFailure(h, wakeSignalDef(t, "drop-sig"),
		wakeErr("something broke", nil))

	require.Eventually(t, sink.sawFailed, 2*time.Second, 10*time.Millisecond,
		"a real wake failure stays loud")
}

// TestHoldTaskGuards covers the human-task hold (SRD-071 FR-8): a volatile
// engine declines it (no checkpoint to wake from), an armed one records which
// TRACK the task parks so an action can wake that instance, and ReleaseWaits
// clears it with the rest of the track's holds.
func TestHoldTaskGuards(t *testing.T) {
	t.Run("a volatile engine holds nothing", func(t *testing.T) {
		th, err := New("engine-voltask",
			WithoutBanner(), WithoutStartupConfig())
		require.NoError(t, err)

		err = th.HoldTask("i-1", "t-1", "task-1")
		require.Error(t, err)
		require.Contains(t, err.Error(), "no checkpoints")
	})

	// An armed engine ACCEPTS the hold and registers nothing for it: a task
	// wakes its instance through the distributor's own routing, so the hold is
	// the answer "yes, this wait is wakeable", not a record (SRD-071 FR-3b).
	t.Run("an armed engine accepts the hold", func(t *testing.T) {
		th, cancel := armedWakeEngine(t, "engine-task")
		defer cancel()

		require.NoError(t, th.HoldTask("i-1", "t-1", "task-1"))
	})
}

// TestRetryAfterHydration classifies the replay signal: only the engine's own
// "I am releasing" refusal asks for a replay — a real action failure does not.
func TestRetryAfterHydration(t *testing.T) {
	require.False(t, retryAfterHydration(nil))
	require.False(t, retryAfterHydration(wakeErr("something broke", nil)))

	require.True(t, retryAfterHydration(gerrs.New(
		gerrs.M("releasing"),
		gerrs.C(errorClass, instance.TaskRetryClass))))
}

// TestSettledForIsPerInstanceID: the terminal signal is owned per instance ID
// and handed to every rebuild, which is what lets a WaitCompletion survive a
// dehydration cycle (SRD-071).
func TestSettledForIsPerInstanceID(t *testing.T) {
	th, cancel := armedWakeEngine(t, "engine-settled")
	defer cancel()

	first := th.settledFor("i-1")
	require.NotNil(t, first)
	require.Equal(t, first, th.settledFor("i-1"),
		"a rebuild must inherit the SAME signal, not a fresh one")

	require.NotEqual(t, first, th.settledFor("i-2"),
		"different instances settle independently")
}

// TestWakeOutcomeLabels: the residency fact reports what the wake led to.
func TestWakeOutcomeLabels(t *testing.T) {
	require.Equal(t, "TaskAction", wakeTriggerLabel(nil))
	require.Equal(t, "all", wokenTrackID(nil))
	require.Equal(t, "t-1", wokenTrackID(&instance.PendingTrigger{
		TrackID: "t-1"}))
}

// TestHydrateForTaskDefersToAnInFlightWake: with a wake already latched for an
// instance, a task action does not start a second rebuild — it lets the
// in-flight one finish and replays against the result (§4.6).
func TestHydrateForTaskDefersToAnInFlightWake(t *testing.T) {
	th, cancel := armedWakeEngine(t, "engine-taskwake")
	defer cancel()

	require.True(t, th.claimWake("i-1"))
	defer th.releaseWake("i-1")

	require.NoError(t, th.hydrateForTask("i-1"),
		"a concurrent wake is deferred to, not duplicated")
}

// TestRebuildRefusesABrokenRecord: a checkpoint whose recorded node is not in
// the pinned process version fails the REBUILD loudly (the wake reports rather
// than resurrecting an instance onto a node that no longer exists).
func TestRebuildRefusesABrokenRecord(t *testing.T) {
	th, cancel := armedWakeEngine(t, "engine-brokennode")
	defer cancel()

	p := shortTimerProcess(t, "wake-brokennode")

	reg, err := th.RegisterProcess(p)
	require.NoError(t, err)

	doc := &checkpoint.Document{
		InstanceID: "broken-node",
		ProcessID:  p.ID(),
		Version:    reg.Version(),
		Status:     "Active",
		Tracks: []checkpoint.TrackRecord{{
			ID:     "t-1",
			State:  "TrackDehydrated",
			NodeID: "a-node-this-version-never-had",
		}},
	}

	payload, err := doc.Marshal()
	require.NoError(t, err)

	require.NoError(t, th.cfg.Repository().Save(context.Background(),
		repository.InstanceRecord{
			ID: "broken-node", Status: repository.StatusActive,
			Payload: payload}))

	err = th.rebuildAndContinue("broken-node", &instance.PendingTrigger{
		TrackID: "t-1", EDef: wakeSignalDef(t, "broken-sig")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "doesn't rebuild")
}

// TestWakeOutcomeReadsTerminalStates: the residency fact distinguishes a wake
// that CONTINUED the flow from one that finished the instance (SRD-071 FR-10).
func TestWakeOutcomeReadsTerminalStates(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	th, cancel := armedWakeEngine(t, "engine-outcome")
	defer cancel()

	p := shortTimerProcess(t, "wake-outcome")

	_, err := th.RegisterProcess(p)
	require.NoError(t, err)

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	inst, err := th.instanceByID(h.ID())
	require.NoError(t, err)

	// still running — the wake would report a continuation.
	require.Equal(t, "continued", wakeOutcome(inst))

	// once it settles, the outcome names the terminal state rather than
	// claiming the flow carried on. Which terminal it is depends on whether
	// the cancel or the flow won — both are "not continued", which is the
	// distinction the fact exists to draw.
	inst.Cancel()

	select {
	case <-inst.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the instance did not settle")
	}

	require.Eventually(t, func() bool {
		return wakeOutcome(inst) != "continued"
	}, 3*time.Second, 10*time.Millisecond,
		"a settled instance must not be reported as continuing")
}

// retryActor is a minimal hi.Actor for the task-action traces.
type retryActor struct{ id string }

func (a retryActor) UserID() string   { return a.id }
func (a retryActor) Groups() []string { return nil }

// TestTaskActionRetryExhausts covers the hydrate-and-replay loop (SRD-071
// FR-8): when an action keeps being refused because the instance has no live
// loop, the engine retries a bounded number of times and then reports — it
// neither spins forever nor returns success for work that never happened.
func TestTaskActionRetryExhausts(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	th, cancel := armedWakeEngine(t, "engine-retry")
	defer cancel()

	p := shortTimerProcess(t, "retry-proc")

	_, err := th.RegisterProcess(p)
	require.NoError(t, err)

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	inst, err := th.instanceByID(h.ID())
	require.NoError(t, err)

	// drive it terminal: its loop is gone, so every task action it is asked to
	// service is refused with the retry class.
	inst.Cancel()

	select {
	case <-inst.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the instance did not stop")
	}

	// point a task at that finished instance — the shape a task action hits
	// when the instance it belongs to has no loop left to run against.
	th.registerTask(interactor.TaskInfo{
		TaskRef: interactor.TaskRef{TaskID: "ghost-task", InstanceID: h.ID()},
	})

	_, err = th.Take(context.Background(), "ghost-task",
		retryActor{id: "operator"})
	require.Error(t, err,
		"a task action that can never be serviced must report, not hang")
}

// TestTwoTimerHoldsCoexist covers the widened hold key (SRD-071 M10): a track
// can own more than one deadline — an Event-Based Gateway racing two timers
// arms one per arm, and a wait guarded by a timer boundary holds its own
// alongside the boundary's.
//
// Keyed by track alone, as it was through M3–M9, the second hold silently
// REPLACED the first. When the lost one was the earlier deadline, the wait
// fired late with nothing in the logs to say why.
func TestTwoTimerHoldsCoexist(t *testing.T) {
	th, cancel := armedWakeEngine(t, "engine-two-timers")
	defer cancel()

	early, late := wakeTimerDef(t), wakeTimerDef(t)

	require.NoError(t, th.HoldTimer("i-1", "t-1", early,
		wakeEpoch.Add(time.Minute), 0, exec.WaitNode))
	require.NoError(t, th.HoldTimer("i-1", "t-1", late,
		wakeEpoch.Add(time.Hour), 0, exec.WaitNode))

	th.timerSvc.mu.Lock()
	held := len(th.timerSvc.holds)
	th.timerSvc.mu.Unlock()

	require.Equal(t, 2, held,
		"both of the track's deadlines are held, not one overwriting the other")

	// the EARLIER deadline is the one the service schedules against.
	got, ok := th.timerSvc.nearest()
	require.True(t, ok)
	require.True(t, wakeEpoch.Add(time.Minute).Equal(got),
		"the nearest deadline must be the earlier of the two")

	// releasing the track withdraws EVERY deadline it owns — the all-or-
	// nothing withdrawal ReleaseWaits performs for its subscriptions too.
	th.ReleaseWaits("i-1", "t-1")

	th.timerSvc.mu.Lock()
	remaining := len(th.timerSvc.holds)
	th.timerSvc.mu.Unlock()

	require.Zero(t, remaining, "a released track leaves no deadline behind")
}
