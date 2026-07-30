package instance

import (
	"context"
	"strconv"
	"time"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
)

// WithCheckpointing arms the instance's consistent-cut checkpoints
// (SRD-070 FR-4): owner is the engine id the ownership lease names,
// ttl its validity window (ADR-033 §2.8). Without this option the
// instance runs volatile, exactly as before — the engine wires it when
// a Repository is meant to hold the state of record. An empty owner or
// non-positive ttl leaves checkpointing off (the option constructors'
// nil-safety posture: never break a working default).
func WithCheckpointing(owner string, ttl time.Duration) Option {
	return func(cfg *newConfig) {
		if owner == "" || ttl <= 0 {
			return
		}

		cfg.cpOwner = owner
		cfg.cpTTL = ttl
	}
}

// checkpointTransitions lists the trackEvent kinds that close an
// observable lifecycle transition — the persist points (ADR-033 §2.2;
// the loop checkpoints AFTER applying one of these).
var checkpointTransitions = map[trackEventKind]bool{
	evEnded:       true,
	evWaiting:     true,
	evTaskWaiting: true,
	evJobWaiting:  true,
	evScopeOpen:   true,
}

// liveTrackStates lists the states a track restores from — everything
// not yet finished or absorbed. Ended/merged/canceled/failed tracks are
// never persisted: their effect lives in the committed data (SRD-070
// §4.1).
var liveTrackStates = map[trackState]bool{
	TrackCreated:            true,
	TrackReady:              true,
	TrackExecutingStep:      true,
	TrackProcessStepResults: true,
	TrackWaitForEvent:       true,
	// AwaitingMerge restores too: the track already ARRIVED at its join,
	// and the join's reachability math needs that arrival — re-entering
	// the join node re-delivers it.
	TrackAwaitingMerge: true,
	TrackAwaitSync:     true,
	// A dehydrated track is a live record whose wait is held externally
	// (SRD-071) — persisted so the checkpoint IS the hydration source.
	TrackDehydrated: true,
}

// maybeCheckpoint captures and saves the instance's document after an
// applied transition (SRD-070 FR-4). Persistence never faults
// execution: an unsupported in-flight construct, an uncodable payload
// or a failed save degrade to the CheckpointDeferred fact at Warn and
// the instance runs on volatile until the next transition retries.
func (ls *loopState) maybeCheckpoint(ctx context.Context, kind trackEventKind) {
	inst := ls.inst
	if inst.cpOwner == "" || !checkpointTransitions[kind] {
		return
	}

	ls.checkpointNow(ctx)
}

// checkpointNow builds and saves the document unconditionally (the
// activation and terminal writes call it directly).
func (ls *loopState) checkpointNow(ctx context.Context) {
	inst := ls.inst
	if inst.cpOwner == "" {
		return
	}

	doc, deferReason := ls.captureDocument(ctx)
	if deferReason != "" {
		inst.reportCheckpointDeferred(deferReason)

		return
	}

	payload, err := doc.Marshal()
	if err != nil {
		inst.reportCheckpointDeferred("marshal: " + err.Error())

		return
	}

	rec := repository.InstanceRecord{
		ID:         inst.ID(),
		Status:     persistedStatus(inst.State()),
		Payload:    payload,
		RecVersion: inst.cpRecVersion,
		Lease: repository.Lease{
			Owner:       inst.cpOwner,
			Incarnation: inst.cpIncarnation,
			Expiry:      inst.now().Add(inst.cpTTL),
		},
	}

	if err := inst.Repository().Save(ctx, rec); err != nil {
		inst.reportCheckpointDeferred("save: " + err.Error())

		return
	}

	inst.cpRecVersion++
}

// captureDocument builds the Schema-1 document from the loop-confined
// state — a consistent cut by construction (the loop is the single
// writer). A non-empty defer reason means the document cannot be
// captured faithfully yet (SRD-070 FR-4's defer-don't-die list).
func (ls *loopState) captureDocument(
	ctx context.Context,
) (*checkpoint.Document, string) {
	inst := ls.inst

	switch {
	case len(ls.calls) > 0:
		return nil, "a Call Activity is in flight"
	case len(ls.miGroups) > 0:
		return nil, "a parallel multi-instance group is in flight"
	case len(ls.sweeps) > 0:
		return nil, "a compensation sweep is in flight"
	}

	doc := &checkpoint.Document{
		InstanceID:  inst.ID(),
		ParentID:    inst.parentInstanceID,
		CallNodeID:  inst.callNodeID,
		ProcessID:   inst.s.ProcessID,
		Version:     inst.s.Version,
		Status:      inst.State().String(),
		ConvKeys:    inst.corr.snapshotKeys(),
		CompletedBy: inst.performers.snapshot(),
	}

	for _, path := range inst.sc.plane.OpenPaths() {
		dd, err := inst.sc.plane.OwnData(path)
		if err != nil {
			return nil, "scope read: " + err.Error()
		}

		raw, err := checkpoint.EncodeData(ctx, string(path), dd)
		if err != nil {
			return nil, "encode: " + err.Error()
		}

		doc.Scopes = append(doc.Scopes,
			checkpoint.ScopeRecord{Path: string(path), Data: raw})
	}

	for path, entries := range ls.ledgers {
		rr, err := ledgerRecords(ctx, string(path), entries)
		if err != nil {
			return nil, "ledger encode: " + err.Error()
		}

		doc.Ledgers = append(doc.Ledgers, rr...)
	}

	for _, t := range inst.tracks {
		if rec, live := trackRecord(t); live {
			doc.Tracks = append(doc.Tracks, rec)
		}
	}

	doc.Boundaries = ls.boundaryRecords()

	return doc, ""
}

// boundaryRecords captures the boundary events armed over the live tracks
// (SRD-071 FR-9a). ls.watchers is loop-owned and the capture runs on the loop,
// so no lock is involved — unlike the track records, whose fields the tracks'
// own goroutines write.
//
// A watch over a track that is no longer live is skipped: the capture writes
// what a restore must rebuild, and a boundary whose host is gone guards
// nothing.
func (ls *loopState) boundaryRecords() []checkpoint.BoundaryRecord {
	var out []checkpoint.BoundaryRecord

	for trackID, ws := range ls.watchers {
		t, live := ls.inst.tracks[trackID]
		if !live || !liveTrackStates[t.currentState()] {
			continue
		}

		for _, w := range ws {
			rec := checkpoint.BoundaryRecord{
				HostTrack:  trackID,
				BoundaryID: w.boundary.ID(),
				DefIndex:   w.defIndex,
			}

			if !w.deadline.IsZero() {
				rec.Timer = &checkpoint.TimerDescriptor{
					Deadline:   w.deadline,
					CyclesLeft: w.cycles,
				}
			}

			out = append(out, rec)
		}
	}

	return out
}

// ledgerRecords flattens one scope's ledger (folded children recurse
// into their own records under the same scope path — the ordinal +
// activity id keep them addressable).
func ledgerRecords(
	ctx context.Context, path string, entries []*ledgerEntry,
) ([]checkpoint.LedgerRecord, error) {
	out := make([]checkpoint.LedgerRecord, 0, len(entries))

	for _, e := range entries {
		snap, err := checkpoint.EncodeData(ctx, path, e.snapshot)
		if err != nil {
			return nil, err
		}

		out = append(out, checkpoint.LedgerRecord{
			ScopePath:  path,
			ActivityID: e.activityID,
			HandlerID:  e.handlerID,
			Snapshot:   snap,
			Ordinal:    e.ordinal,
		})

		folded, err := ledgerRecords(ctx, path, e.folded)
		if err != nil {
			return nil, err
		}

		out = append(out, folded...)
	}

	return out, nil
}

// trackRecord captures one live track; live=false skips it. Every read
// rides the track mutex: the capture runs on the loop goroutine while
// the track's own goroutine may be mid-arming (writes guarded on its
// side too).
func trackRecord(t *track) (checkpoint.TrackRecord, bool) {
	t.m.RLock()
	defer t.m.RUnlock()

	if !liveTrackStates[t.state] {
		return checkpoint.TrackRecord{}, false
	}

	rec := checkpoint.TrackRecord{
		ID:          t.ID(),
		State:       t.state.String(),
		NodeID:      t.steps[len(t.steps)-1].node.ID(),
		ScopePath:   string(t.scopePath),
		ScopeSeg:    t.scopeSeg,
		TaskID:      t.taskID,
		Prev:        append([]string{}, t.prev...),
		MsgDefIDs:   append([]string{}, t.msgDefIDs...),
		LoopCounter: t.loopCounter,
	}

	if t.state == TrackWaitForEvent && !t.timerDeadline.IsZero() {
		rec.Timer = &checkpoint.TimerDescriptor{
			Deadline:   t.timerDeadline,
			CyclesLeft: t.timerCycles,
		}
	}

	return rec, true
}

// persistedStatus maps the runtime lifecycle onto the repository's
// persisted status vocabulary.
var persistedStatusMap = map[State]repository.Status{
	Created:     repository.StatusActive,
	Active:      repository.StatusActive,
	Completed:   repository.StatusCompleted,
	Terminating: repository.StatusTerminated,
	Terminated:  repository.StatusTerminated,
	// a dehydrated instance is in-flight, not terminal and not
	// operator-suspended — recovery claims it as an active record and wakes
	// it from its checkpoint (SRD-071 FR-2).
	Dehydrated: repository.StatusActive,
}

func persistedStatus(s State) repository.Status {
	if st, ok := persistedStatusMap[s]; ok {
		return st
	}

	return repository.StatusActive
}

// reportCheckpointDeferred emits the FR-8 degradation fact: the
// instance keeps running, the operator sees durability is off.
func (inst *Instance) reportCheckpointDeferred(reason string) {
	inst.report(observability.Fact{
		Kind:  observability.KindInstanceState,
		Phase: observability.PhaseCheckpointDeferred,
		Details: map[string]string{
			"reason":                    reason,
			observability.AttrProcessID: inst.s.ProcessID,
			observability.AttrVersion:   strconv.Itoa(inst.s.Version),
		},
	})
}
