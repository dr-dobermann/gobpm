package instance

import (
	"context"
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

// Restore rebuilds an instance from its checkpoint document over a
// fresh clone of the PINNED process version (SRD-070 FR-6): the
// recorded scopes reopen and their data recommits, conversation keys
// and the compensation ledger rebuild, and every recorded live track
// respawns at its node with RE-ENTER semantics — subscriptions
// re-register, tasks re-announce, jobs re-enqueue (the ADR-033 §2.3
// at-least-once effects); a recorded timer re-arms at its RECORDED
// deadline through the DeadlineHinter seam.
func Restore(
	doc *checkpoint.Document,
	s *snapshot.Snapshot,
	parentRoot scope.DataPath,
	er engrenv.EngineRuntime,
	ep eventproc.EventProducer,
	td interactor.TaskDistributor,
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

	if err := inst.restoreLedgers(ctx, doc); err != nil {
		return nil, err
	}

	if err := inst.restoreTracks(doc); err != nil {
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
					errs.D("scope_path", rec.Path),
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
				errs.D("scope_path", rec.Path),
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
// the loop to spawn — the re-enter respawn.
func (inst *Instance) restoreTracks(doc *checkpoint.Document) error {
	for i := range doc.Tracks {
		rec := &doc.Tracks[i]

		node, ok := inst.s.Nodes[rec.NodeID]
		if !ok {
			return errs.New(
				errs.M("Restore: the recorded node isn't in the pinned "+
					"process version"),
				errs.C(errorClass, errs.ObjectNotFound),
				errs.D("node_id", rec.NodeID),
				errs.D("track_id", rec.ID))
		}

		t, err := restoredTrack(node, inst, rec)
		if err != nil {
			return err
		}

		inst.tracks[t.ID()] = t
		inst.addToSnap(t)
	}

	inst.trackCount.Store(int64(len(inst.tracks)))

	return nil
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
			errs.D("track_id", rec.ID),
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
		evtCh:       make(chan flow.EventDefinition, eventBufferDepth),
		scopePath:   scope.DataPath(rec.ScopePath),
		scopeSeg:    rec.ScopeSeg,
		loopCounter: rec.LoopCounter,
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
