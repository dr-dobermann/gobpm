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

	// recovery affinity (ADR-033 v.5 §2.10, SRD-087 FR-1): a child is
	// never revived on its own — recovery reaches it only through its
	// caller's claim, so two engines of one group can never split a
	// pair between them.
	for _, id := range t.recoveryRoots(ctx, ids) {
		if _, err := t.recoverOne(ctx, id, map[string]struct{}{}); err != nil {
			t.reportRecoveryFailure(id, err)
		}
	}
}

// recoveryRoots drops every listed instance that names a caller
// (SRD-087 FR-1), preserving the listing order of the rest: a child is
// reached only through its caller's claim, never revived on its own —
// so no outcome depends on a parent and a child coinciding in one
// listing. A child whose caller is TERMINAL is finished here instead
// (FR-6, the interrupted cascade). An unreadable or undecodable record
// is kept as a root: recoverOne is the one place that reports such a
// failure, and silently skipping it would lose the instance AND the
// fact.
func (t *Thresher) recoveryRoots(
	ctx context.Context, ids []string,
) []string {
	repo := t.cfg.Repository()
	roots := make([]string, 0, len(ids))

	for _, id := range ids {
		rec, ok, err := repo.Load(ctx, id)
		if err != nil || !ok {
			roots = append(roots, id)

			continue
		}

		doc, derr := checkpoint.Unmarshal(rec.Payload)
		if derr != nil {
			roots = append(roots, id)

			continue
		}

		if doc.ParentID == "" {
			roots = append(roots, id)

			continue
		}

		// a child: its caller's claim revives it — unless that caller
		// is terminal, when the cascade is finished here (FR-6).
		if err := t.finishOrphanedChild(ctx, rec, doc.ParentID); err != nil {
			t.reportRecoveryFailure(id, err)
		}
	}

	return roots
}

// finishOrphanedChild completes an interrupted cancel cascade
// (SRD-087 FR-6, ADR-033 v.5 §2.10): a parent completes only after its
// call returns and a terminating parent terminates its children, so a
// TERMINAL caller with a live child means the cascade did not finish
// before the crash. The child's record is written terminal and
// reported — never revived (its outcome has no consumer), never left
// (a permanent resident of every later listing). A caller that is
// still in flight, or unreadable, leaves the child untouched for its
// caller's claim.
func (t *Thresher) finishOrphanedChild(
	ctx context.Context, rec repository.InstanceRecord, parentID string,
) error {
	repo := t.cfg.Repository()

	prec, ok, err := repo.Load(ctx, parentID)
	if err != nil {
		return recoveryErr("the caller record isn't readable", err)
	}

	if !ok {
		// the SRD-082 orphan refusal: a child never runs orphaned. The
		// record stays for an operator; the fact is the report.
		return recoveryErr("the caller record "+strconv.Quote(parentID)+
			" is gone — a child never runs orphaned", nil)
	}

	if !prec.Status.IsTerminal() {
		// the caller is in flight: normally its own claim revives this
		// child in this same sweep. But a caller LIVE ON ANOTHER ENGINE
		// is not in this sweep at all, so nobody would revive the child
		// — report it rather than dropping it silently (SRD-087 FR-3's
		// posture: a tree that cannot be recovered is said out loud).
		if !prec.Lease.Expired(t.cfg.Clock().Now()) &&
			prec.Lease.Owner != t.id {
			return recoveryErr("its caller "+strconv.Quote(parentID)+
				" is live on engine "+strconv.Quote(prec.Lease.Owner)+
				" — that engine owns the call tree and this child is "+
				"not revived here", nil)
		}

		return nil // the caller's own claim revives this child
	}

	rec.Status = repository.StatusTerminated

	if err := repo.Save(ctx, rec); err != nil {
		return recoveryErr("couldn't finish the interrupted cancel "+
			"cascade of caller "+strconv.Quote(parentID), err)
	}

	t.producer.Report(observability.Fact{
		Kind:  observability.KindInstanceState,
		Phase: observability.PhaseTerminated,
		Details: map[string]string{
			observability.AttrInstanceID: rec.ID,
			"reason":                     "caller-terminal",
			"caller":                     parentID,
		},
	})

	return nil
}

// claimRecord loads one record and takes it under a HIGHER incarnation
// — the fencing token every later save carries (ADR-033 §2.8). claimed
// is false without an error when the record is legitimately not ours to
// recover: another engine holds a live lease, or won the CAS race.
func (t *Thresher) claimRecord(
	ctx context.Context, id string,
) (repository.InstanceRecord, bool, error) {
	repo := t.cfg.Repository()
	now := t.cfg.Clock().Now()

	rec, ok, err := repo.Load(ctx, id)
	if err != nil || !ok {
		return rec, false,
			recoveryErr("the record vanished before the claim", err)
	}

	if !rec.Lease.Expired(now) {
		// another engine claimed it between list and load
		return rec, false, nil
	}

	// A cross-group record reached by id is a wiring mistake, not a
	// race — refuse loud (SRD-078 FR-2; the listing is group-scoped, so
	// this guards direct-id paths and misbehaving stores alike).
	if rec.Group != t.group {
		return rec, false, recoveryErr("the instance belongs to engine group "+
			strconv.Quote(rec.Group)+", this engine runs in "+
			strconv.Quote(t.group), nil)
	}

	rec.Lease = repository.Lease{
		Owner:       t.id,
		Incarnation: rec.Lease.Incarnation + 1,
		Expiry:      now.Add(t.cfg.leaseTTL),
	}

	if saveErr := repo.Save(ctx, rec); saveErr != nil {
		// ONLY a CAS mismatch is a lost race — the Repository contract
		// classifies it errs.ConcurrentUpdate, and every adapter mirrors
		// that. Any other failure (an unreachable store, a broken
		// connection) must not masquerade as one: swallowing it would
		// abandon a recoverable instance without a word.
		var ae *gerrs.ApplicationError
		if errors.As(saveErr, &ae) && ae.HasClass(gerrs.ConcurrentUpdate) {
			return rec, false, nil
		}

		return rec, false, recoveryErr("the claim doesn't save", saveErr)
	}

	rec.RecVersion++ // Save advanced the stored version; continue from it

	return rec, true, nil
}

// recoverOne claims and rehydrates a single instance. seen carries the
// ids already visited by THIS recovery walk (SRD-087 FR-5): it is
// threaded through the call-tree recursion, so a document naming its
// own ancestor terminates on the guard rather than on the incidental
// fact that a claimed lease is skipped.
func (t *Thresher) recoverOne(
	ctx context.Context, id string, seen map[string]struct{},
) (bool, error) {
	// a revisit reports CLAIMED: this walk already owns the id, and the
	// caller's affinity check must not read "someone else holds it".
	if _, visited := seen[id]; visited {
		return true, nil
	}

	seen[id] = struct{}{}

	repo := t.cfg.Repository()

	rec, claimed, err := t.claimRecord(ctx, id)
	if err != nil || !claimed {
		return false, err
	}

	doc, err := checkpoint.Unmarshal(rec.Payload)
	if err != nil {
		return false, recoveryErr("the checkpoint doesn't decode", err)
	}

	// a recovered CHILD whose caller record vanished fails loud — a
	// call is recorded state on both ends, and a child must never run
	// orphaned (ADR-033 v.4 §2.10, SRD-082 FR-7).
	if doc.ParentID != "" {
		_, ok, perr := repo.Load(ctx, doc.ParentID)
		if perr != nil {
			return false, recoveryErr("the caller record isn't readable", perr)
		}

		if !ok {
			return false, recoveryErr("the caller record "+
				strconv.Quote(doc.ParentID)+" is gone — a child never "+
				"runs orphaned", nil)
		}
	}

	// affinity (SRD-087 FR-2/FR-4): claim and restore the awaited call
	// tree BEFORE this instance, so its re-attach finds every child
	// resident on this engine instead of live on another.
	if terr := t.recoverCallTree(ctx, doc, seen); terr != nil {
		return false, terr
	}

	s := t.snapshotForVersionLocked(doc.ProcessID, doc.Version)
	if s == nil {
		return false, recoveryErr("the pinned process version isn't registered "+
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
		return false, recoveryErr("the instance doesn't restore", err)
	}

	runCtx, cancel, err := t.instanceContext("recovery")
	if err != nil {
		return false, recoveryErr("the engine context is gone", err)
	}
	if err := inst.Run(runCtx); err != nil {
		cancel()

		return false, recoveryErr("the restored instance doesn't run", err)
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

	return true, nil
}

// recoverCallTree claims and restores every child the document awaits,
// depth-first (SRD-087 FR-2/FR-4/FR-5): each child is recovered by the
// same recoverOne — which recurses into ITS children — so a whole tree
// lands on one engine before its root restores. seen guards a
// malformed document naming its own ancestor. A child that is already
// resident (recovered earlier in this sweep) is skipped; a child whose
// lease is genuinely LIVE fails the tree loud, naming it and its
// holder — tearing down another engine's running instance to satisfy
// this recovery would destroy real state (§2.10).
func (t *Thresher) recoverCallTree(
	ctx context.Context,
	doc *checkpoint.Document,
	seen map[string]struct{},
) error {
	repo := t.cfg.Repository()
	now := t.cfg.Clock().Now()

	for _, call := range doc.Calls {
		childID := call.ChildID

		// recoverOne owns the seen bookkeeping (it marks on entry and
		// returns early for a revisit) — marking here too would make
		// every child look already-visited and skip its recovery.
		if _, visited := seen[childID]; visited {
			continue
		}

		// instanceByID reports ObjectNotFound when the child is NOT
		// resident; a nil error therefore means it already recovered in
		// this sweep and needs no claim.
		if _, lookupErr := t.instanceByID(childID); lookupErr == nil {
			continue
		}

		rec, ok, err := repo.Load(ctx, childID)
		if err != nil {
			return recoveryErr("the awaited child record isn't readable", err)
		}

		if !ok || rec.Status.IsTerminal() {
			// a vanished record is the SRD-082 refusal (raised by the
			// re-attach itself); a terminal child needs no claim — its
			// record IS the outcome the re-attach reads.
			continue
		}

		if !rec.Lease.Expired(now) && rec.Lease.Owner != t.id {
			return recoveryErr("the awaited child "+strconv.Quote(childID)+
				" is live on engine "+strconv.Quote(rec.Lease.Owner)+
				" — a call tree recovers as a unit (ADR-033 §2.10)", nil)
		}

		claimed, err := t.recoverOne(ctx, childID, seen)
		if err != nil {
			return recoveryErr("the awaited child "+strconv.Quote(childID)+
				" doesn't recover", err)
		}

		// the lease check above is not enough: another engine can claim
		// the child in the window between it and our own CAS. An
		// unclaimed child is NOT recovered, and letting the parent
		// restore anyway trades the affinity message for whatever its
		// re-attach happens to say (SRD-087 FR-3).
		if !claimed {
			return recoveryErr("the awaited child "+strconv.Quote(childID)+
				" was claimed by another engine mid-sweep — a call tree "+
				"recovers as a unit (ADR-033 §2.10)", nil)
		}
	}

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
