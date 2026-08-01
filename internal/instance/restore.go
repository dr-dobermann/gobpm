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
	inst.restoreStartedAt(doc.StartedAt)

	if err := inst.restoreLedgers(ctx, doc); err != nil {
		return nil, err
	}

	if err := inst.restoreTracks(doc, pending); err != nil {
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

		if _, err := inst.sc.plane.Commit(path, dd...); err != nil {
			return errs.New(
				errs.M("Restore: couldn't recommit scope data"),
				errs.C(errorClass, errs.OperationFailed),
				errs.D(observability.AttrScopePath, rec.Path),
				errs.E(err))
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

	for _, rec := range doc.Ledgers {
		var err error

		entry := &ledgerEntry{
			activityID: rec.ActivityID,
			handlerID:  rec.HandlerID,
			ordinal:    rec.Ordinal,
		}

		if len(rec.Snapshot) > 0 {
			entry.snapshot, err = checkpoint.DecodeData(ctx, rec.Snapshot)
			if err != nil {
				return err
			}
		}

		path := scope.DataPath(rec.ScopePath)
		inst.restoredLedgers[path] = append(inst.restoredLedgers[path], entry)
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

	for i := range doc.Tracks {
		rec := &doc.Tracks[i]

		node, ok := inst.s.Nodes[rec.NodeID]
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
	}

	if rec.Timer != nil {
		t.timerDeadline = rec.Timer.Deadline
		t.timerCycles = rec.Timer.CyclesLeft
		t.timerHinted = true
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
