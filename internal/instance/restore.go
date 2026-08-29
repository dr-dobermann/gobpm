package instance

import (
	"context"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	engrenv "github.com/dr-dobermann/gobpm/pkg/renv"
)

// WithCheckpointCursor seeds the restored instance's CAS record version
// and lease incarnation from the claimed record (SRD-070 FR-7) — the
// engine passes what its claim advanced them to, so the instance's next
// checkpoint continues the fencing chain.
func WithCheckpointCursor(recVersion, incarnation int64) Option {
	return func(cfg *newConfig) {
		cfg.cpRecVersion = recVersion
		cfg.cpIncarnation = incarnation
	}
}

// CorrelationDropClass marks a wake refused because its message belongs to
// another conversation (ADR-016) — a BENIGN outcome the engine logs and moves
// on from, never an instance failure.
const CorrelationDropClass = "WAKE_CORRELATION_DROP"

// PendingTrigger carries a trigger that accompanies a hydration, turning
// a cold RE-ENTER into a wake-on-trigger CONTINUATION (ADR-007 v.2 §2.3,
// SRD-071 FR-4). The single discriminator of Restore's two modes:
//   - pending == nil — cold restart (SRD-070): the woken wait re-ARMS
//     (subscriptions re-register, a timer re-arms at its recorded deadline).
//   - pending != nil — wake: the named track re-enters its wait node with
//     the trigger already in hand and FIRES THROUGH it, never re-arming, as
//     a continuation fork (its persisted prev inherits the dehydrated
//     track's lineage without appending it — bounded across cycles, §4.1).
type PendingTrigger struct {
	// TrackID is the dehydrated track being woken (its recorded node is the
	// wait node the continuation fork re-enters).
	TrackID string
	// EDef is the trigger (a timer/message/signal definition, or a synthetic
	// completion event for a human task); nil means trigger-absent.
	EDef flow.EventDefinition
	// Data is prepared node input — a task's completion outputs — committed
	// into the woken track's scope before it fires; nil for a timer (a
	// message binds its payload through the node's ProcessEvent).
	Data []data.Data
}

// Restore rebuilds an instance from its checkpoint document over a
// fresh clone of the PINNED process version (SRD-070 FR-6): the
// recorded scopes reopen and their data recommits, conversation keys
// and the compensation ledger rebuild, and every recorded live track
// respawns at its node with RE-ENTER semantics — subscriptions
// re-register, tasks re-announce, jobs re-enqueue (the ADR-033 §2.3
// at-least-once effects); a recorded timer re-arms at its RECORDED
// deadline through the DeadlineHinter seam.
//
// A non-nil pending turns the RE-ENTER into a wake-on-trigger CONTINUATION
// for the named track (ADR-007 v.2 §2.3, SRD-071 FR-4): it fires through
// the wait node with the trigger in hand instead of re-arming, while its
// still-waiting siblings re-arm normally.
func Restore(
	doc *checkpoint.Document,
	s *snapshot.Snapshot,
	parentRoot scope.DataPath,
	er engrenv.EngineRuntime,
	ep eventproc.EventProducer,
	td interactor.TaskDistributor,
	pending *PendingTrigger,
	opts ...Option,
) (*Instance, error) {
	if doc == nil {
		return nil, errs.New(
			errs.M("Restore: a nil Document isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if s != nil && s.Version != doc.Version {
		return nil, errs.New(
			errs.M("Restore: the snapshot isn't the pinned version"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("pinned_version", itoa(doc.Version)),
			errs.D("snapshot_version", itoa(s.Version)))
	}

	opts = append(opts,
		withRestoredIdentity(doc.InstanceID),
		withCallLinkage(doc.ParentID, doc.CallNodeID))

	inst, err := New(s, parentRoot, er, ep, td, opts...)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()

	if err := inst.restoreScopes(ctx, doc); err != nil {
		return nil, err
	}

	inst.corr.restoreKeys(doc.ConvKeys)
	inst.performers.restore(doc.CompletedBy)
	inst.iterationOwners.restore(doc.IterationOwners)
	inst.iterations.restore(doc.Iterations)
	inst.restoreStartedAt(doc.StartedAt)

	if err := inst.restoreLedgers(ctx, doc); err != nil {
		return nil, err
	}

	if err := inst.restoreTracks(doc, pending); err != nil {
		return nil, err
	}

	// the parallel open sets, the resolving sweeps, the in-flight calls
	// and the ad-hoc routing states ride to the loop's adoption (SRD-082
	// FR-4/FR-6/FR-7, SRD-083 FR-3) — the substrates are loop-owned.
	inst.restoredGroups = doc.MIGroups
	inst.restoredSweeps = doc.Sweeps
	inst.restoredCalls = doc.Calls
	inst.restoredAdHoc = doc.AdHoc

	// the scope table rides too, for the half of it the data plane does
	// not carry: OpenPaths replays the paths, but only the record says
	// which track opened each and which ordinal it is (Schema 7,
	// SRD-090.A M3c).
	inst.restoredScopes = doc.Scopes

	// a recorded caller re-PARKS instead of re-invoking its child — the
	// adoption re-links to the recorded child; a second InvokeProcess
	// would duplicate the child instance (SRD-082 FR-7).
	for i := range doc.Calls {
		if tr, ok := inst.tracks[doc.Calls[i].TrackID]; ok {
			tr.callRestored = true
		}
	}

	// a recorded wait-throw thrower re-PARKS instead of re-entering its
	// throw node — a second evCompensate would double-compensate
	// (SRD-082 FR-6). Marked here, before any track goroutine exists.
	for i := range doc.Sweeps {
		if id := doc.Sweeps[i].ThrowerTrack; id != "" {
			if tr, ok := inst.tracks[id]; ok {
				tr.compWaitRestored = true
			}
		}
	}

	if err := inst.restoreIncidents(doc); err != nil {
		return nil, err
	}

	return inst, nil
}

// withRestoredIdentity keeps the instance's recorded id and switches New
// into restore mode (no start-track seeding — Restore rebuilds the
// track table itself).
func withRestoredIdentity(id string) Option {
	return func(cfg *newConfig) {
		cfg.restoredID = id
	}
}

// restoreScopes reopens the recorded scope tree (parent-first) and
// recommits the recorded data — overriding the freshly seeded property
// values with the checkpointed ones.
func (inst *Instance) restoreScopes(
	ctx context.Context, doc *checkpoint.Document,
) error {
	recs := append([]checkpoint.ScopeRecord{}, doc.Scopes...)
	sort.Slice(recs, func(i, j int) bool {
		return strings.Count(recs[i].Path, "/") < strings.Count(recs[j].Path, "/")
	})

	for _, rec := range recs {
		path := scope.DataPath(rec.Path)

		if path != inst.sc.root {
			if err := inst.sc.plane.OpenScope(path); err != nil {
				return errs.New(
					errs.M("Restore: couldn't reopen scope"),
					errs.C(errorClass, errs.OperationFailed),
					errs.D(observability.AttrScopePath, rec.Path),
					errs.E(err))
			}
		}

		if len(rec.Data) == 0 {
			continue
		}

		dd, err := checkpoint.DecodeData(ctx, rec.Data)
		if err != nil {
			return err
		}

		if len(dd) == 0 {
			continue
		}

		// Every way Commit can fail is already excluded upstream: the path was
		// OpenScope'd immediately above (which checks containment and
		// writability), and DecodeData's NewParameter rejects a nil item or an
		// empty name before batchNames could. A failure here is a broken
		// invariant, not a corrupt checkpoint.
		if _, err := inst.sc.plane.Commit(path, dd...); err != nil {
			return errs.Invariant("scope %q: recommit failed: %w", rec.Path, err)
		}
	}

	return nil
}

// restoreLedgers rebuilds the compensation ledger the loop adopts at
// start. The capture flattened folded children (SRD-070 M3); they
// restore as sibling entries under their recorded scope path — full
// folded fidelity rides SRD-071+.
func (inst *Instance) restoreLedgers(
	ctx context.Context, doc *checkpoint.Document,
) error {
	if len(doc.Ledgers) == 0 {
		return nil
	}

	inst.restoredLedgers = map[scope.DataPath][]*ledgerEntry{}

	for i := range doc.Ledgers {
		rec := &doc.Ledgers[i]

		entry, err := ledgerEntryFromRecord(ctx, rec)
		if err != nil {
			return err
		}

		path := scope.DataPath(rec.ScopePath)
		inst.restoredLedgers[path] = append(inst.restoredLedgers[path], entry)
	}

	return nil
}

// ledgerEntryFromRecord rebuilds one ledger entry — the shared decoder
// of the ledger table and the sweep queues (SRD-082 FR-6).
func ledgerEntryFromRecord(
	ctx context.Context, rec *checkpoint.LedgerRecord,
) (*ledgerEntry, error) {
	entry := &ledgerEntry{
		activityID:      rec.ActivityID,
		activityName:    rec.ActivityName,
		handlerID:       rec.HandlerID,
		handlerName:     rec.HandlerName,
		ordinal:         rec.Ordinal,
		handlerEventSub: rec.HandlerEventSub,
	}

	if len(rec.Snapshot) > 0 {
		var err error

		entry.snapshot, err = checkpoint.DecodeData(ctx, rec.Snapshot)
		if err != nil {
			return nil, err
		}
	}

	return entry, nil
}

// legacyLeafInstances names the track records a SCHEMA-5 document carries for
// the instances of a parallel LEAF Multi-Instance, which must not be rebuilt
// as tracks (SRD-090.A FR-7).
//
// Schema 5 spawned a track per instance of a parallel leaf and kept it out of
// the iteration routing with a `leafPlain` marker. SRD-090.A M2b made a leaf
// instance an execution rather than a track and deleted both — so restoring
// such a record now produces a track standing on the iterated node, which
// reaches execFor, builds a decorator of its OWN over the same activity, and
// fans the whole thing out again. The body runs N extra times per stray
// record, and nothing fails: the work is simply done more than once.
//
// The host is the one the group names; every other record on the same node is
// an instance. A COMPOSITE group is untouched — its instances were child
// scopes, which adoptRestoredGroups rebuilds from the group's Open paths, and
// its host is the only track that was ever recorded for the node.
//
// The records are not merely dropped: for a leaf they ARE the executor set,
// since the group's Open list names scopes and a leaf instance had none. Each
// carries its ordinal as its LoopCounter, so the second return maps a group's
// host to the ordinals still running — which adoptRestoredGroups needs to
// resume exactly those and no others.
func legacyLeafInstances(
	inst *Instance, doc *checkpoint.Document,
) (map[string]bool, map[string][]int) {
	if len(doc.MIGroups) == 0 {
		return nil, nil
	}

	legacy := map[string]bool{}
	ordinals := map[string][]int{}

	for i := range doc.MIGroups {
		grp := &doc.MIGroups[i]

		host := trackRecordByID(doc, grp.HostTrack)
		if host == nil {
			continue // the refusal for this belongs to adoptRestoredGroups
		}

		node, ok := inst.s.NodeByID(host.NodeID)
		if !ok {
			continue
		}

		if _, composite := node.(scopeHost); composite {
			continue // its instances were scopes, not tracks
		}

		for j := range doc.Tracks {
			rec := &doc.Tracks[j]
			if rec.NodeID == host.NodeID && rec.ID != grp.HostTrack {
				legacy[rec.ID] = true
				ordinals[grp.HostTrack] = append(
					ordinals[grp.HostTrack], rec.LoopCounter)
			}
		}

		sort.Ints(ordinals[grp.HostTrack])
	}

	return legacy, ordinals
}

// trackRecordByID finds a track record by id, or nil.
func trackRecordByID(
	doc *checkpoint.Document, id string,
) *checkpoint.TrackRecord {
	for i := range doc.Tracks {
		if doc.Tracks[i].ID == id {
			return &doc.Tracks[i]
		}
	}

	return nil
}

// restoreTracks rebuilds the live tracks at their recorded nodes for
// the loop to spawn — the re-enter respawn. When pending names a track,
// that one becomes a wake-on-trigger continuation fork (fires through its
// wait node) while its siblings re-arm normally (SRD-071 FR-4).
func (inst *Instance) restoreTracks(
	doc *checkpoint.Document, pending *PendingTrigger,
) error {
	// The armed boundaries go in BEFORE the tracks: the loop arms a track's
	// boundaries as it spawns it, and each arm looks its recorded plan up here
	// so a duration-based deadline is restored rather than recomputed
	// (SRD-071 FR-9a). A Schema-1 document carries none and every boundary
	// simply re-arms fresh, which is what that document was written expecting.
	inst.seedBoundaryPlans(doc.Boundaries)

	legacy, ordinals := legacyLeafInstances(inst, doc)
	inst.restoredLeafOrdinals = ordinals

	for i := range doc.Tracks {
		rec := &doc.Tracks[i]

		// a Schema-5 parallel LEAF instance is not a track any more
		// (SRD-090.A FR-7) — rebuilding it as one would re-decorate the
		// activity, see below.
		if legacy[rec.ID] {
			continue
		}

		node, ok := inst.s.NodeByID(rec.NodeID)
		if !ok {
			return errs.New(
				errs.M("Restore: the recorded node isn't in the pinned "+
					"process version"),
				errs.C(errorClass, errs.ObjectNotFound),
				errs.D(observability.AttrNodeID, rec.NodeID),
				errs.D(observability.AttrTrackID, rec.ID))
		}

		var (
			t   *track
			err error
		)

		if pending != nil && rec.ID == pending.TrackID {
			t, err = inst.continuationTrack(node, rec, pending)
		} else {
			t, err = restoredTrack(node, inst, rec)
		}

		if err != nil {
			return err
		}

		inst.tracks[t.ID()] = t
		inst.addToSnap(t)
	}

	inst.trackCount.Store(int64(len(inst.tracks)))

	return nil
}

// continuationTrack builds the wake-on-trigger continuation fork for a
// dehydrated wait (ADR-007 v.2 §2.3, SRD-071 FR-4): a FRESH track re-entering
// the recorded wait node with the trigger already loaded in evtCh. It is NOT
// re-armed — recordBornWaiter skips a woken track — so run() reads the
// preloaded trigger and fires straight through the node to the outgoing flow.
// Its persisted prev INHERITS the dehydrated track's lineage without appending
// the dehydrated track id, so repeated dehydrate/wake cycles do not grow it
// (§4.1). Prepared node input (a task's outputs) is committed into the track's
// scope before it fires; a message binds its own payload in the node's
// ProcessEvent, a timer has none.
func (inst *Instance) continuationTrack(
	node flow.Node,
	rec *checkpoint.TrackRecord,
	pending *PendingTrigger,
) (*track, error) {
	be, err := foundation.NewBaseElement()
	if err != nil {
		return nil, errs.New(
			errs.M("Restore: couldn't mint the continuation-fork identity"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D(observability.AttrTrackID, rec.ID),
			errs.E(err))
	}

	t := track{
		BaseElement: *be,
		prev:        append([]string{}, rec.Prev...),
		steps: []*stepInfo{
			{node: node, state: StepCreated},
		},
		state:       TrackWaitForEvent,
		woken:       true,
		instance:    inst,
		parkCh:      make(chan struct{}, 1),
		dehydrateCh: make(chan struct{}),
		evtCh:       make(chan flow.EventDefinition, eventBufferDepth),
		scopePath:   scope.DataPath(rec.ScopePath),
		scopeSeg:    rec.ScopeSeg,
		loopCounter: rec.LoopCounter,
		// a woken track may be an ad-hoc-routed activity: keep the
		// assignment, or its settle would skip the Router (SRD-083 FR-3).
		adHocActivity: rec.AdHocActivity,
	}

	if len(pending.Data) > 0 {
		if _, err := inst.sc.plane.Commit(
			t.scopePath, pending.Data...); err != nil {
			return nil, errs.New(
				errs.M("Restore: couldn't commit the trigger's prepared input"),
				errs.C(errorClass, errs.OperationFailed),
				errs.D(observability.AttrTrackID, rec.ID),
				errs.E(err))
		}
	}

	// A WOKEN ITERATED ACTIVITY keeps its executor set, exactly as a track
	// restored the ordinary way does (SRD-090.A FR-7). Without it the
	// decorator resumes from zero — re-running instances a restore had
	// already found complete — and, for a fan-out over human work, every
	// instance mints a fresh parked-work identity, so the task handles the
	// distributor is holding name nothing (ADR-020 §2.12).
	//
	// It is easy to miss here because a continuation is built from the wake
	// rather than from the record, and everything else it needs travels on
	// the trigger.
	t.iterSeed = rec.Iteration

	// NOT t.taskID. That is the track's SINGLE slot, and humanTaskIdentity
	// hands it to adoptTaskID for every ordinal — so seeding it here would
	// make all N instances of a fan-out adopt ONE identity, which is the
	// defect this work exists to remove. A fan-out's per-instance ids come
	// back through rec.Iteration.Instances instead.

	if rec.Iteration != nil {
		t.miSeed = &checkpoint.MIRecord{
			N:            rec.Iteration.N,
			Completed:    rec.Iteration.Completed,
			ConditionMet: rec.Iteration.ConditionMet,
			Staging:      rec.Iteration.Staging,
		}
	} else {
		t.miSeed = rec.MI
	}

	if pending.EDef == nil {
		return nil, errs.New(
			errs.M("Restore: a wake needs a trigger definition"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D(observability.AttrTrackID, rec.ID))
	}

	// A woken MESSAGE runs the same correlation rule a resident delivery does
	// (ADR-016) — the continuation fork bypasses the loop's dispatch gate, so
	// it is applied here instead, on the rebuilt instance: the conversation
	// keys this message derives are ASSOCIATED (a first message joins the
	// conversation), and a mismatch refuses the wake. The engine's holder-side
	// gate is the cheap early-out that avoids rebuilding for a foreign
	// conversation; this is the authoritative decision.
	if pending.EDef.Type() == flow.TriggerMessage &&
		inst.corr.validateAndAssociate(context.Background(), pending.EDef) {
		return nil, errs.New(
			errs.M("Restore: the trigger belongs to another conversation"),
			errs.C(errorClass, CorrelationDropClass),
			errs.D(observability.AttrTrackID, rec.ID),
			errs.D(observability.AttrEventDefinitionID, pending.EDef.ID()))
	}

	// preload the trigger: run() enters awaitTrigger, reads it, and fires the
	// node through deliver() — the exact resident fire path, minus the wait.
	t.evtCh <- pending.EDef

	return &t, nil
}

// restoredTrack builds one track at its recorded position.
func restoredTrack(
	node flow.Node, inst *Instance, rec *checkpoint.TrackRecord,
) (*track, error) {
	be, err := foundation.NewBaseElement(foundation.WithID(rec.ID))
	if err != nil {
		return nil, errs.New(
			errs.M("Restore: couldn't rebuild track identity"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D(observability.AttrTrackID, rec.ID),
			errs.E(err))
	}

	t := track{
		BaseElement: *be,
		prev:        append([]string{}, rec.Prev...),
		steps: []*stepInfo{
			{node: node, state: StepCreated},
		},
		state:       TrackReady,
		instance:    inst,
		parkCh:      make(chan struct{}, 1),
		dehydrateCh: make(chan struct{}),
		evtCh:       make(chan flow.EventDefinition, eventBufferDepth),
		scopePath:   scope.DataPath(rec.ScopePath),
		scopeSeg:    rec.ScopeSeg,
		loopCounter: rec.LoopCounter,
		// the recorded human-task id: parkHumanTask REUSES it rather than
		// minting, so the id a human holds survives rehydration (SRD-071 FR-8).
		taskID: rec.TaskID,
		// the recorded routed-activity assignment: the adoption rebuilds
		// the container's running counts from it (SRD-083 FR-3).
		adHocActivity: rec.AdHocActivity,
	}

	if rec.Timer != nil {
		t.timerDeadline = rec.Timer.Deadline
		t.timerCycles = rec.Timer.CyclesLeft
		t.timerHinted = true
	}

	// a recorded own-iteration position: the decorator resumes at the
	// recorded pass instead of iterating from zero (SRD-082 FR-3).
	//
	// A LEAF activity's set is Iteration (Schema 6, SRD-090.A FR-6/FR-7);
	// a Schema-5 document has only the MI mirror, which for a leaf meant
	// the same sequential position — so it seeds the same field and the
	// old document restores to the same live state (FR-7).
	t.miSeed = rec.MI
	t.iterSeed = rec.Iteration

	if rec.Iteration != nil {
		t.miSeed = &checkpoint.MIRecord{
			N:            rec.Iteration.N,
			Completed:    rec.Iteration.Completed,
			ConditionMet: rec.Iteration.ConditionMet,
			Staging:      rec.Iteration.Staging,
		}
	}

	if err := t.checkNodeType(node, true); err != nil {
		return nil, err
	}

	return &t, nil
}

// TimerDeadlineHint implements the waiters.DeadlineHinter capability
// (SRD-070 FR-6): a restored track pins its recorded firing plan; a
// normally-armed track hints nothing and the waiter evaluates as usual.
func (t *track) TimerDeadlineHint(string) (time.Time, int, bool) {
	if !t.timerHinted {
		return time.Time{}, 0, false
	}

	return t.timerDeadline, t.timerCycles, true
}

func itoa(v int) string { return strconv.Itoa(v) }
