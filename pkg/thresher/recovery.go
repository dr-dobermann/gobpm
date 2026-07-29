package thresher

import (
	"context"
	"strconv"

	gerrs "github.com/dr-dobermann/gobpm/pkg/errs"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
)

// recoverInstances claims and rehydrates every claimable in-flight
// instance (SRD-070 FR-7): list → CAS-claim (incarnation+1, the fencing
// token) → decode → resolve the PINNED registered version → Restore →
// run. Failures are per-instance and loud (an operator-visible fact);
// they never block the engine start or the other instances.
func (t *Thresher) recoverInstances(ctx context.Context) {
	repo := t.cfg.Repository()

	ids, err := repo.ListInFlight(ctx, t.cfg.Clock().Now())
	if err != nil {
		t.cfg.logger.Warn("recovery: couldn't list in-flight instances",
			"error", err.Error())

		return
	}

	for _, id := range ids {
		if err := t.recoverOne(ctx, id); err != nil {
			t.reportRecoveryFailure(id, err)
		}
	}
}

// recoverOne claims and rehydrates a single instance.
func (t *Thresher) recoverOne(ctx context.Context, id string) error {
	repo := t.cfg.Repository()
	now := t.cfg.Clock().Now()

	rec, ok, err := repo.Load(ctx, id)
	if err != nil || !ok {
		return recoveryErr("the record vanished before the claim", err)
	}

	if !rec.Lease.Expired(now) {
		return nil // another engine claimed it between list and load
	}

	// The claim: our ownership under a HIGHER incarnation — the fencing
	// token every later save carries (ADR-033 §2.8). A lost CAS race is
	// not an error: someone else recovered it.
	rec.Lease = repository.Lease{
		Owner:       t.id,
		Incarnation: rec.Lease.Incarnation + 1,
		Expiry:      now.Add(t.cfg.leaseTTL),
	}

	if saveErr := repo.Save(ctx, rec); saveErr != nil {
		return nil //nolint:nilerr // a lost claim race is the normal outcome
	}

	rec.RecVersion++ // Save advanced the stored version; continue from it

	doc, err := checkpoint.Unmarshal(rec.Payload)
	if err != nil {
		return recoveryErr("the checkpoint doesn't decode", err)
	}

	s := t.snapshotForVersionLocked(doc.ProcessID, doc.Version)
	if s == nil {
		return recoveryErr("the pinned process version isn't registered "+
			"(process "+doc.ProcessID+" v"+strconv.Itoa(doc.Version)+
			") — register it before Run", nil)
	}

	// cold restart: no pending trigger — the recorded waits re-ARM (a timer
	// re-arms at its recorded deadline). Wake-on-trigger passes a PendingTrigger.
	inst, err := instance.Restore(doc, s, scope.EmptyDataPath, &t.cfg, t,
		t.taskDist,
		nil,
		instance.WithSettledSignal(t.settledFor(id)),
		instance.WithInvoker(t),
		instance.WithWaitHolders(t),
		instance.WithCheckpointing(t.id, t.cfg.leaseTTL),
		instance.WithCheckpointCursor(rec.RecVersion, rec.Lease.Incarnation))
	if err != nil {
		return recoveryErr("the instance doesn't restore", err)
	}

	runCtx, cancel := context.WithCancel(t.ctx)
	if err := inst.Run(runCtx); err != nil {
		cancel()

		return recoveryErr("the restored instance doesn't run", err)
	}

	t.trackInstanceLocked(inst, cancel, t.settledFor(id))

	inst.Report(observability.Fact{
		Kind:  observability.KindInstanceState,
		Phase: observability.PhaseRecovered,
		Details: map[string]string{
			observability.AttrProcessID: doc.ProcessID,
			observability.AttrVersion:   strconv.Itoa(doc.Version),
			"live_tracks":               strconv.Itoa(len(doc.Tracks)),
		},
	})

	return nil
}

// recoveryErr builds one classified recovery error.
func recoveryErr(msg string, cause error) error {
	if cause == nil {
		return gerrs.New(
			gerrs.M("recovery: "+msg),
			gerrs.C(errorClass, gerrs.OperationFailed))
	}

	return gerrs.New(
		gerrs.M("recovery: "+msg),
		gerrs.C(errorClass, gerrs.OperationFailed),
		gerrs.E(cause))
}

// reportRecoveryFailure surfaces one instance's failed recovery — the
// instance stays unclaimed/failed, the rest recover (SRD-070 FR-7).
func (t *Thresher) reportRecoveryFailure(id string, err error) {
	t.cfg.logger.Warn("recovery failed for instance",
		"instance_id", id, "error", err.Error())

	t.producer.Report(observability.Fact{
		Kind:  observability.KindInstanceState,
		Phase: observability.PhaseFailed,
		Details: map[string]string{
			"instance_id":           id,
			"reason":                "recovery",
			observability.AttrError: err.Error(),
		},
	})
}
