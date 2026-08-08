package thresher

import (
	"context"
	"errors"
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

	ids, err := repo.ListInFlight(ctx, t.group, t.cfg.Clock().Now())
	if err != nil {
		t.cfg.logger.Warn("recovery: couldn't list in-flight instances",
			observability.AttrError, err.Error())

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

	// A cross-group record reached by id is a wiring mistake, not a
	// race — refuse loud (SRD-078 FR-2; the listing is group-scoped, so
	// this guards direct-id paths and misbehaving stores alike).
	if rec.Group != t.group {
		return recoveryErr("the instance belongs to engine group "+
			strconv.Quote(rec.Group)+", this engine runs in "+
			strconv.Quote(t.group), nil)
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
		// A LOST CAS is the normal outcome — another engine recovered it — and
		// is silence. Anything else is not: a connection reset, a timeout or a
		// store outage used to be read the same way, so a transient failure
		// abandoned an in-flight instance at startup and reported success, with
		// nothing logged anywhere (FIX-038 §1.4). The Repository contract makes
		// the two distinguishable: a version mismatch MUST carry
		// errs.ConcurrentUpdate, and every adapter implements it identically.
		if lostClaim(saveErr) {
			return nil
		}

		return recoveryErr("couldn't claim the record for recovery", saveErr)
	}

	rec.RecVersion++ // Save advanced the stored version; continue from it

	doc, err := checkpoint.Unmarshal(rec.Payload)
	if err != nil {
		return recoveryErr("the checkpoint doesn't decode", err)
	}

	// a recovered CHILD whose caller record vanished fails loud — a
	// call is recorded state on both ends, and a child must never run
	// orphaned (ADR-033 v.4 §2.10, SRD-082 FR-7).
	if doc.ParentID != "" {
		_, ok, perr := repo.Load(ctx, doc.ParentID)
		if perr != nil {
			return recoveryErr("the caller record isn't readable", perr)
		}

		if !ok {
			return recoveryErr("the caller record "+
				strconv.Quote(doc.ParentID)+" is gone — a child never "+
				"runs orphaned", nil)
		}
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
		instance.WithCallReattacher(t.reattachChild),
		instance.WithWaitHolders(t),
		instance.WithCheckpointing(t.id, t.group, t.cfg.leaseTTL),
		instance.WithCheckpointCursor(rec.RecVersion, rec.Lease.Incarnation))
	if err != nil {
		return recoveryErr("the instance doesn't restore", err)
	}

	runCtx, cancel, err := t.instanceContext("recovery")
	if err != nil {
		return recoveryErr("the engine context is gone", err)
	}
	if err := inst.Run(runCtx); err != nil {
		cancel()

		return recoveryErr("the restored instance doesn't run", err)
	}

	_, displaced := t.trackInstanceLocked(inst, cancel, t.settledFor(id))
	stopDisplaced(displaced)

	// A recovered conversation re-takes its correlation reservation (FIX-036
	// §1.2): the reservation map does not survive the process, so without this
	// the next message carrying this instance's key would start a duplicate
	// beside the one just recovered.
	t.rebindKeysLocked(doc.ProcessID, id, doc.ConvKeys)

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

// ensureGroup establishes — or, under WithExistingEngineGroup, asserts
// — the engine's group in the repository's registry at startup
// (SRD-078 FR-2, ADR-033 v.3 §2.8).
func (t *Thresher) ensureGroup(ctx context.Context) error {
	repo := t.cfg.Repository()

	if !t.cfg.groupJoinOnly {
		if err := repo.RegisterGroup(ctx, t.group); err != nil {
			return gerrs.New(
				gerrs.M("couldn't register engine group %q", t.group),
				gerrs.C(errorClass, gerrs.OperationFailed),
				gerrs.E(err))
		}

		return nil
	}

	ok, err := repo.GroupExists(ctx, t.group)
	if err != nil {
		return gerrs.New(
			gerrs.M("couldn't check engine group %q", t.group),
			gerrs.C(errorClass, gerrs.OperationFailed),
			gerrs.E(err))
	}

	if !ok {
		return gerrs.New(
			gerrs.M("engine group %q isn't established in the repository"+
				" (WithExistingEngineGroup joins existing groups only)", t.group),
			gerrs.C(errorClass, gerrs.ObjectNotFound))
	}

	return nil
}

// lostClaim reports whether err is the compare-and-set conflict that means
// another engine claimed this instance first — the one Save failure that is a
// normal outcome rather than a fault.
func lostClaim(err error) bool {
	var ae *gerrs.ApplicationError

	return errors.As(err, &ae) && ae.HasClass(gerrs.ConcurrentUpdate)
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
		observability.AttrInstanceID, id, observability.AttrError, err.Error())

	t.producer.Report(observability.Fact{
		Kind:  observability.KindInstanceState,
		Phase: observability.PhaseFailed,
		Details: map[string]string{
			observability.AttrInstanceID: id,
			"reason":                     "recovery",
			observability.AttrError:      err.Error(),
		},
	})
}
