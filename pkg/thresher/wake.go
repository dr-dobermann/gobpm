package thresher

import (
	"context"
	"strconv"
	"time"

	gerrs "github.com/dr-dobermann/gobpm/pkg/errs"
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
) error {
	if t.timerSvc == nil {
		return gerrs.New(
			gerrs.M("HoldTimer: the engine timer service isn't running"),
			gerrs.C(errorClass, gerrs.InvalidState))
	}

	t.timerSvc.hold(timerHold{
		instanceID: instanceID,
		trackID:    trackID,
		eDef:       eDef,
		deadline:   deadline,
		cycles:     cycles,
	})

	return nil
}

// hydrateFromTimer is the timer service's wake callback (SRD-071 FR-4): a held
// deadline fired, so continue the instance with the timer as its pending
// trigger. A failure is per-instance and loud — it never crashes the service.
func (t *Thresher) hydrateFromTimer(
	instanceID string, pending *instance.PendingTrigger,
) {
	if err := t.wakeInstance(instanceID, pending); err != nil {
		t.reportWakeFailure(instanceID, err)
	}
}

// wakeInstance forks on residency (ADR-007 v.2 §2.4): a RESIDENT instance still
// has its loop, so the trigger delivers into the live parked track; a
// DEHYDRATED one is rebuilt from its checkpoint and its woken wait continues as
// a fork. Single-flight (§4.6): the wake latch serializes concurrent triggers
// for one instance, so it hydrates exactly once.
func (t *Thresher) wakeInstance(
	instanceID string, pending *instance.PendingTrigger,
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

	if !t.claimWake(instanceID) {
		// another wake is already hydrating this instance — it will deliver
		// the trigger into the soon-resident loop; nothing to do here (§4.6).
		return nil
	}
	defer t.releaseWake(instanceID)

	return t.rebuildAndContinue(instanceID, pending)
}

// rebuildAndContinue rebuilds a dehydrated instance from its checkpoint and
// continues its woken wait with the pending trigger (SRD-071 FR-4). It mirrors
// recoverOne's claim→restore→run, but drives the RE-ENTER as a wake: the
// PendingTrigger fires the woken node through instead of re-arming it. The
// re-claim (incarnation+1) fences a zombie writer exactly as restart recovery
// does (ADR-033 §2.8).
func (t *Thresher) rebuildAndContinue(
	instanceID string, pending *instance.PendingTrigger,
) error {
	ctx := t.ctx

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

	inst, err := instance.Restore(doc, s, scope.EmptyDataPath, &t.cfg, t,
		t.taskDist,
		pending,
		instance.WithInvoker(t),
		instance.WithWaitHolders(t),
		instance.WithCheckpointing(t.id, t.cfg.leaseTTL),
		instance.WithCheckpointCursor(rec.RecVersion, rec.Lease.Incarnation))
	if err != nil {
		return wakeErr("the instance doesn't rebuild", err)
	}

	runCtx, cancel := context.WithCancel(t.ctx)
	if err := inst.Run(runCtx); err != nil {
		cancel()

		return wakeErr("the rebuilt instance doesn't run", err)
	}

	t.trackInstanceLocked(inst, cancel)

	inst.Report(observability.Fact{
		Kind:  observability.KindInstanceState,
		Phase: observability.PhaseHydrated,
		Details: map[string]string{
			observability.AttrProcessID: doc.ProcessID,
			observability.AttrVersion:   strconv.Itoa(doc.Version),
			"trigger":                   string(pending.EDef.Type()),
		},
	})

	return nil
}

// wakeClaimAttempts bounds the CAS retry below — a couple of rounds absorb the
// dehydration write; more would mean something else is fighting for the record.
const wakeClaimAttempts = 3

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

// claimWake latches an instance's wake, returning false when one is already in
// flight (single-flight, SRD-071 §4.6).
func (t *Thresher) claimWake(instanceID string) bool {
	t.wakeMu.Lock()
	defer t.wakeMu.Unlock()

	if t.waking[instanceID] {
		return false
	}

	t.waking[instanceID] = true

	return true
}

// releaseWake clears an instance's wake latch.
func (t *Thresher) releaseWake(instanceID string) {
	t.wakeMu.Lock()
	delete(t.waking, instanceID)
	t.wakeMu.Unlock()
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
		"instance_id", instanceID, "error", err.Error())

	t.producer.Report(observability.Fact{
		Kind:  observability.KindInstanceState,
		Phase: observability.PhaseFailed,
		Details: map[string]string{
			"instance_id":           instanceID,
			"reason":                "wake",
			observability.AttrError: err.Error(),
		},
	})
}
