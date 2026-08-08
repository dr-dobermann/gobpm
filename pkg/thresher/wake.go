package thresher

import (
	"context"
	"strconv"
	"time"

	gerrs "github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/scope"
)

// HoldTimer implements exec.WaitHolders (SRD-071 FR-6): a dehydratable timer
// registers its absolute deadline with the engine timer service at arm time, so
// the timer fires — and wakes the instance — even after it releases its
// goroutines. Without a running service (checkpointing off), it declines, and
// the caller keeps the wait resident on an in-hub waiter.
func (t *Thresher) HoldTimer(
	instanceID, trackID string,
	eDef flow.EventDefinition,
	deadline time.Time,
	cycles int,
	kind exec.WaitKind,
) error {
	if t.timerSvc == nil {
		return gerrs.New(
			gerrs.M("HoldTimer: the engine timer service isn't running"),
			gerrs.C(errorClass, gerrs.InvalidState))
	}

	// Announce the arm BEFORE building it, so a ReleaseWaits landing between
	// here and the hold below can cancel it (FIX-037 §1.5). Arming blind let a
	// release that found nothing be followed by a hold that registered a
	// deadline for the wait it had just canceled.
	token := t.timerSvc.beginArm(instanceID, trackID)

	if !t.timerSvc.hold(timerHold{
		instanceID: instanceID,
		trackID:    trackID,
		eDef:       eDef,
		deadline:   deadline,
		cycles:     cycles,
		kind:       kind,
	}, token) {
		t.reportRefusedArm(instanceID, trackID)
	}

	return nil
}

// reportRefusedArm records an arm the timer service refused because a release
// withdrew the track's waits while it was in flight. It is NOT a failure: the
// release is authoritative and there is nothing left to hold, exactly as a
// withdrawn subscription hold reports success (FIX-037 §1.5).
func (t *Thresher) reportRefusedArm(instanceID, trackID string) {
	t.cfg.logger.Debug("timer hold refused — the wait was released mid-arm",
		observability.AttrInstanceID, instanceID, observability.AttrTrackID, trackID)
}

// hydrateFromTimer is the timer service's wake callback (SRD-071 FR-4): a held
// deadline fired, so continue the instance with the timer as its pending
// trigger. A failure is per-instance and loud — it never crashes the service —
// and is REPORTED BACK, so the service keeps the deadline and retries it rather
// than discarding the instance's only way back (FIX-027 §3.2.1).
func (t *Thresher) hydrateFromTimer(
	instanceID string, pending *instance.PendingTrigger,
) bool {
	err := t.wakeInstance(instanceID, pending)
	if err != nil {
		t.reportWakeFailure(instanceID, err)

		return false
	}

	return true
}

// wakeInstance forks on residency (ADR-007 v.2 §2.4): a RESIDENT instance still
// has its loop, so the trigger delivers into the live parked track; a
// DEHYDRATED one is rebuilt from its checkpoint and its woken wait continues as
// a fork. Single-flight (§4.6): the wake latch serializes concurrent triggers
// for one instance, so it hydrates exactly once.
func (t *Thresher) wakeInstance(
	instanceID string, pending *instance.PendingTrigger,
) error {
	return t.wakeInstanceAttempt(instanceID, pending, 0)
}

// wakeInstanceAttempt is wakeInstance's body, carrying the attempt count so a
// caller that lost the latch can wait for the in-flight wake and retry its own
// delivery a BOUNDED number of times.
func (t *Thresher) wakeInstanceAttempt(
	instanceID string, pending *instance.PendingTrigger, attempt int,
) error {
	// Still resident? Deliver into the live loop (today's path, reached through
	// the holder instead of a hub waiter). The instance may release its
	// goroutines between this check and the delivery, so the delivery itself
	// reports whether the loop took it; if it did not, fall through and wake the
	// instance from its checkpoint — the trigger is never lost to that race
	// (SRD-071 NFR-1).
	if inst, err := t.instanceByID(instanceID); err == nil &&
		inst.State() != instance.Dehydrated {
		delivered, err := inst.WakeParkedTrack(pending.TrackID, pending.EDef)
		if err != nil {
			return err
		}

		if delivered {
			return nil
		}
	}

	done, claimed := t.claimWake(instanceID)
	if claimed {
		defer t.releaseWake(instanceID)

		return t.rebuildAndContinue(instanceID, pending)
	}

	// Another wake is rebuilding the instance. It carries ITS OWN trigger and
	// cannot deliver this one, so returning here would drop the event and
	// report success to a caller that treats nil as delivered — the hub
	// reports nothing, the timer service surrenders the deadline (FIX-037
	// §1.1). Wait for that wake, then retry this trigger from the top: the
	// instance is resident by then, so the delivery above takes it.
	if !t.awaitWake(done) {
		return wakeErr("the engine stopped while awaiting an in-flight wake", nil)
	}

	// Bounded, so a storm of concurrent wakes cannot recurse without end.
	// Exhausting the attempts is LOUD: the trigger could not be delivered, and
	// silence is exactly what made the original defect invisible.
	if attempt+1 >= wakeDeliverAttempts {
		return wakeErr("the trigger could not be delivered after "+
			strconv.Itoa(wakeDeliverAttempts)+" attempts — the instance kept "+
			"being rebuilt by concurrent wakes", nil)
	}

	return t.wakeInstanceAttempt(instanceID, pending, attempt+1)
}

// rebuildAndContinue rebuilds a dehydrated instance from its checkpoint and
// continues its woken wait with the pending trigger (SRD-071 FR-4). It mirrors
// recoverOne's claim→restore→run, but drives the RE-ENTER as a wake: the
// PendingTrigger fires the woken node through instead of re-arming it. The
// re-claim (incarnation+1) fences a zombie writer exactly as restart recovery
// does (ADR-033 §2.8).
func (t *Thresher) rebuildAndContinue(
	instanceID string,
	pending *instance.PendingTrigger,
	extra ...instance.Option,
) error {
	// One atomic load serves the whole wake: the claim, the rebuild and the
	// run all belong to the same engine incarnation (FIX-036 §1.1).
	ctx, running := t.engineContext()
	if !running {
		return t.errEngineNotRunning("rebuildAndContinue")
	}

	rec, err := t.claimForWake(ctx, instanceID)
	if err != nil {
		return err
	}

	doc, err := checkpoint.Unmarshal(rec.Payload)
	if err != nil {
		return wakeErr("the checkpoint doesn't decode", err)
	}

	s := t.snapshotForVersionLocked(doc.ProcessID, doc.Version)
	if s == nil {
		return wakeErr("the pinned process version isn't registered "+
			"(process "+doc.ProcessID+" v"+strconv.Itoa(doc.Version)+")", nil)
	}

	opts := append([]instance.Option{
		instance.WithSettledSignal(t.settledFor(instanceID)),
		instance.WithInvoker(t),
		instance.WithCallReattacher(t.reattachChild),
		instance.WithWaitHolders(t),
		instance.WithCheckpointing(t.id, t.group, t.cfg.leaseTTL),
		instance.WithCheckpointCursor(rec.RecVersion, rec.Lease.Incarnation),
	}, extra...)

	inst, err := instance.Restore(doc, s, scope.EmptyDataPath, &t.cfg, t,
		t.taskDist, pending, opts...)
	if err != nil {
		return wakeErr("the instance doesn't rebuild", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	if err := inst.Run(runCtx); err != nil {
		cancel()

		return wakeErr("the rebuilt instance doesn't run", err)
	}

	// ONLY now — the wake has committed. A TRIGGERED wake ends the woken wait,
	// so every hold the DEHYDRATED track owned goes, keyed to its RECORDED id
	// (the continuation fork replacing it is a fresh track, SRD-071 FR-3a). For
	// an Event-Based Gateway this is the withdraw-the-losing-siblings step.
	//
	// It sits BELOW the fallible part deliberately (FIX-027 §2.2): withdrawing
	// first meant a Restore/Run failure took the instance's only way back with
	// it, stranding it in the store as in-flight with nothing left to wake it.
	// Canary: TestFailedRebuildKeepsTheSubscriptionSet.
	//
	// A trigger-ABSENT hydration (a human task action, FR-8) is NOT the end of
	// the wait — the track re-enters its node and re-parks, re-taking its holds
	// — so nothing is withdrawn for it.
	if pending != nil {
		t.ReleaseWaits(instanceID, pending.TrackID)
	}

	h, displaced := t.trackInstanceLocked(inst, cancel, t.settledFor(instanceID))
	stopDisplaced(displaced)

	// A rebuild replaces the instance OBJECT; the handle's observers must
	// follow it or the host's subscription goes quiet (FIX-038 §1.8). Outside
	// the engine lock: AddObserver takes the instance's own observer lock.
	h.reattachObservers()

	// A hydrated conversation re-takes its correlation reservation, for the
	// same reason a recovered one does (FIX-036 §1.2).
	t.rebindKeysLocked(doc.ProcessID, instanceID, doc.ConvKeys)

	inst.Report(observability.Fact{
		Kind:  observability.KindInstanceState,
		Phase: observability.PhaseHydrated,
		Details: map[string]string{
			observability.AttrProcessID: doc.ProcessID,
			observability.AttrVersion:   strconv.Itoa(doc.Version),
			"trigger":                   wakeTriggerLabel(pending),
			observability.AttrTrackID:   wokenTrackID(pending),
			"outcome":                   wakeOutcome(inst),
		},
	})

	return nil
}

// wokenTrackID names the wait the trigger woke, or "all" for a trigger-absent
// hydration, which re-enters every recorded track rather than one (SRD-071
// FR-10).
func wokenTrackID(pending *instance.PendingTrigger) string {
	if pending == nil {
		return "all"
	}

	return pending.TrackID
}

// wakeOutcome reports whether the wake CONTINUED the flow or finished the
// instance outright (SRD-071 FR-10) — the difference between "this trigger
// moved it along" and "this trigger was the last thing it was waiting for",
// which is what an operator reading a residency trace wants to know.
//
// Sampled WITHOUT blocking: this runs on the waking goroutine — the engine's
// single timer service, in the common case — so waiting for the instance to
// settle would serialize every other pending wake behind it. An instance that
// finishes a moment after this reads "continued", and its own Completed fact
// lands immediately after; nothing is lost, and the fast path stays fast.
func wakeOutcome(inst *instance.Instance) string {
	switch inst.State() {
	case instance.Completed:
		return "completed"

	case instance.Terminated, instance.Terminating:
		return "terminated"

	default:
		return "continued"
	}
}

// wakeTriggerLabel names what woke the instance — the trigger's kind, or the
// trigger-absent hydration a human task action performs (SRD-071 FR-8).
func wakeTriggerLabel(pending *instance.PendingTrigger) string {
	if pending == nil || pending.EDef == nil {
		return "TaskAction"
	}

	return string(pending.EDef.Type())
}

// wakeClaimAttempts bounds the CAS retry below — a couple of rounds absorb the
// dehydration write; more would mean something else is fighting for the record.
const wakeClaimAttempts = 3

// wakeDeliverAttempts bounds how many times a trigger that lost the wake latch
// waits for the in-flight rebuild and re-tries its own delivery (FIX-037 §1.1).
// Each round costs one rebuild's wait, and a third failure means something is
// re-parking the instance as fast as it wakes — a condition to report, not to
// keep retrying.
const wakeDeliverAttempts = 3

// claimForWake takes ownership of a dehydrated instance's record so it can be
// rebuilt: load it, re-claim it under a HIGHER incarnation (the fencing token
// every later save carries, ADR-033 §2.8), and return the claimed record with
// its version advanced past the claim.
//
// It RETRIES a lost CAS. Unlike restart recovery — where losing the race means
// another engine legitimately took the instance and dropping out is correct —
// the wake path races the instance's OWN final dehydration checkpoint: the loop
// writes version N+1 between our load at N and our save. Treating that as
// "someone else won" would silently swallow the trigger, leaving a dehydrated
// instance nothing will ever wake again (a stuck instance, not a lost message).
// Re-reading and re-claiming resolves it.
func (t *Thresher) claimForWake(
	ctx context.Context, instanceID string,
) (repository.InstanceRecord, error) {
	repo := t.cfg.Repository()

	var lastErr error

	for range wakeClaimAttempts {
		rec, ok, err := repo.Load(ctx, instanceID)
		if err != nil || !ok {
			return rec, wakeErr("the checkpoint vanished before the wake", err)
		}

		// A cross-group record is a wiring mistake, never a race —
		// refuse loud instead of claiming another cluster's instance
		// (SRD-078 FR-2).
		if rec.Group != t.group {
			return repository.InstanceRecord{},
				wakeErr("the instance belongs to engine group "+
					strconv.Quote(rec.Group)+", this engine runs in "+
					strconv.Quote(t.group), nil)
		}

		rec.Lease = repository.Lease{
			Owner:       t.id,
			Incarnation: rec.Lease.Incarnation + 1,
			Expiry:      t.cfg.Clock().Now().Add(t.cfg.leaseTTL),
		}

		if err := repo.Save(ctx, rec); err != nil {
			lastErr = err

			continue // the record moved under us — re-read and re-claim
		}

		rec.RecVersion++ // Save advanced the stored version; continue from it

		return rec, nil
	}

	return repository.InstanceRecord{},
		wakeErr("couldn't claim the record for the wake", lastErr)
}

// claimWake latches an instance's wake (single-flight, SRD-071 §4.6).
//
// The winner gets ok=true and MUST releaseWake when it is done. A loser gets
// ok=false and a channel closed when the in-flight wake finishes — it waits on
// that and then retries its OWN path, because the latch only says the instance
// is being rebuilt by someone else. It says nothing about the caller's payload,
// and every caller has one: a pending trigger, a residency pin, an operator's
// incident operation. Treating a refusal as "my work is done too" is what lost
// them (FIX-037 §1.1-1.3).
func (t *Thresher) claimWake(instanceID string) (<-chan struct{}, bool) {
	t.wakeMu.Lock()
	defer t.wakeMu.Unlock()

	if done, inFlight := t.waking[instanceID]; inFlight {
		return done, false
	}

	t.waking[instanceID] = make(chan struct{})

	return nil, true
}

// awaitClaim takes the wake latch for instanceID, waiting for an in-flight wake
// when the first attempt loses and retrying a bounded number of times. On a nil
// return the caller OWNS the latch and must releaseWake.
//
// It is the shared form of "I need to rebuild this instance, and someone else
// may be rebuilding it right now" — the shape every rebuild path needs and only
// some of them had (FIX-037 §1.3).
func (t *Thresher) awaitClaim(instanceID, op string) error {
	for range wakeDeliverAttempts {
		done, claimed := t.claimWake(instanceID)
		if claimed {
			return nil
		}

		if !t.awaitWake(done) {
			return t.errEngineNotRunning(op)
		}
	}

	return gerrs.New(
		gerrs.M("%s: instance %q is being rebuilt concurrently", op, instanceID),
		gerrs.C(errorClass, gerrs.OperationFailed))
}

// awaitWake blocks until the in-flight wake signaled by done completes, or the
// engine stops. It reports whether the wake finished — a false means the engine
// is going away and the caller must not retry.
func (t *Thresher) awaitWake(done <-chan struct{}) bool {
	engCtx, running := t.engineContext()
	if !running {
		return false
	}

	select {
	case <-done:
		return true

	case <-engCtx.Done():
		return false
	}
}

// releaseWake clears an instance's wake latch.
func (t *Thresher) releaseWake(instanceID string) {
	t.wakeMu.Lock()
	done := t.waking[instanceID]
	delete(t.waking, instanceID)
	t.wakeMu.Unlock()

	// Closing releases every caller that lost the claim and is waiting to
	// retry its own path.
	if done != nil {
		close(done)
	}
}

// wakeErr builds one classified wake error.
func wakeErr(msg string, cause error) error {
	if cause == nil {
		return gerrs.New(
			gerrs.M("wake: "+msg),
			gerrs.C(errorClass, gerrs.OperationFailed))
	}

	return gerrs.New(
		gerrs.M("wake: "+msg),
		gerrs.C(errorClass, gerrs.OperationFailed),
		gerrs.E(cause))
}

// reportWakeFailure surfaces one instance's failed wake — the timer fired but
// the instance could not be continued (a decode/registration failure). Loud,
// never silent, never fatal to the service.
func (t *Thresher) reportWakeFailure(instanceID string, err error) {
	t.cfg.logger.Warn("wake failed for instance",
		observability.AttrInstanceID, instanceID, observability.AttrError, err.Error())

	t.producer.Report(observability.Fact{
		Kind:  observability.KindInstanceState,
		Phase: observability.PhaseFailed,
		Details: map[string]string{
			observability.AttrInstanceID: instanceID,
			"reason":                     "wake",
			observability.AttrError:      err.Error(),
		},
	})
}
