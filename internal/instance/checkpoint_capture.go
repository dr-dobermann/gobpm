package instance

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
)

// WithCheckpointing arms the instance's consistent-cut checkpoints
// (SRD-070 FR-4): owner is the engine id the ownership lease names,
// group the engine's group every record is stamped with (SRD-078 FR-2,
// ADR-033 §2.8 — never empty for a grouped write), ttl the lease's
// validity window. Without this option the instance runs volatile,
// exactly as before — the engine wires it when a Repository is meant
// to hold the state of record. An empty owner/group or non-positive
// ttl leaves checkpointing off (the option constructors' nil-safety
// posture: never break a working default).
func WithCheckpointing(owner, group string, ttl time.Duration) Option {
	return func(cfg *newConfig) {
		if owner == "" || group == "" || ttl <= 0 {
			return
		}

		cfg.cpOwner = owner
		cfg.cpGroup = group
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
	// a Call Activity park is a persist point since the call is
	// recorded (SRD-082 FR-7, M4): the parent's document must carry the
	// in-flight call the moment the child exists — a crash between the
	// launch and the next transition would otherwise restore a parent
	// that re-invokes.
	evCallWaiting: true,
	// an incident raise is a persist point (SRD-079 FR-5): an incident that
	// vanished with the process would be no incident at all.
	evIncident: true,
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
	// An iterating or scope-hosting track is live and working (ADR-025
	// §2.13b.1e): its activity is mid-flight, so the document must carry it
	// or a restore would lose the token altogether.
	TrackIterating:    true,
	TrackHostingScope: true,
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
		ID:      inst.ID(),
		Status:  persistedStatusOf(inst),
		Payload: payload,
		// The partition keys (SRD-078 FR-1/FR-2): the engine's group;
		// the tenant stays the default ("") until the Multi-tenancy ADR
		// wires real assignment.
		Group:      inst.cpGroup,
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

	// No capture-deferral guards remain (SRD-082 FR-8): every composite
	// construct records its position — groups since M2, sweeps since
	// M3, in-flight calls since M4. Deferral now means only a real
	// encode/save failure, still loud.

	doc := &checkpoint.Document{
		InstanceID:  inst.ID(),
		ParentID:    inst.parentInstanceID,
		CallNodeID:  inst.callNodeID,
		ProcessID:   inst.s.ProcessID,
		Version:     inst.s.Version,
		Status:      inst.State().String(),
		ConvKeys:    inst.corr.snapshotKeys(),
		CompletedBy: inst.performers.snapshot(),
		StartedAt:   inst.startedAtRFC3339(),
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

		rec := checkpoint.ScopeRecord{Path: string(path), Data: raw}

		// who opened it (Schema 7, SRD-090.A M3c). The loop's own entry
		// already knows, so recording it costs one field and spares
		// restore the search that needed a precedence rule. The root
		// scope and any path with no entry are left unnamed — the same
		// "nothing to rebuild" reading a Schema-6 document gets.
		if entry, ok := ls.scopes[path]; ok && entry.host != nil {
			rec.HostTrack = entry.host.ID()

			// -1 marks a host's OWN scope; an instance carries its
			// 0-based ordinal. The two are distinguished here rather
			// than by re-reading the path, which is the whole point.
			rec.Ordinal = -1
			if entry.instance {
				rec.Ordinal = entry.ordinal
			}
		}

		doc.Scopes = append(doc.Scopes, rec)
	}

	for path, entries := range ls.ledgers {
		rr, err := ledgerRecords(ctx, string(path), entries)
		if err != nil {
			return nil, "ledger encode: " + err.Error()
		}

		doc.Ledgers = append(doc.Ledgers, rr...)
	}

	for _, t := range inst.tracks {
		// a sweep handler's track is fully represented by its
		// SweepRecord.Running — restoring it as a plain track TOO would
		// run the handler twice (SRD-082 FR-6).
		if _, isHandler := ls.sweeps[t.ID()]; isHandler {
			continue
		}

		rec, live, err := trackRecord(ctx, t, ls.iter[t.ID()])
		if err != nil {
			return nil, "encode: " + err.Error()
		}

		if live {
			doc.Tracks = append(doc.Tracks, rec)
		}
	}

	sweeps, encErr := ls.sweepRecords(ctx)
	if encErr != "" {
		return nil, encErr
	}

	doc.Sweeps = sweeps
	doc.Calls = ls.callRecords()
	doc.AdHoc = ls.adHocRecords()
	doc.Boundaries = ls.boundaryRecords()
	doc.Incidents = inst.incidentRecords()

	return doc, ""
}

// adHocRecords captures every open Ad-Hoc container's routing state
// (SRD-083 FR-2). All of it — the progress maps, the offer, the flags
// — is loop-owned, so the capture is a consistent cut by construction.
// The running counts are deliberately absent: each routed track's
// record carries its AdHocActivity, and restore rebuilds the counts
// from the track table rather than trusting two tables to agree.
func (ls *loopState) adHocRecords() []checkpoint.AdHocRecord {
	out := make([]checkpoint.AdHocRecord, 0, len(ls.scopes))

	for path, entry := range ls.scopes {
		if entry.adHoc == nil {
			continue
		}

		rec := checkpoint.AdHocRecord{
			HostTrack:  entry.host.ID(),
			ScopePath:  string(path),
			Completed:  copyCounts(entry.adHoc.completed),
			StopReason: entry.adHoc.stopReason,
			Stopped:    entry.adHoc.stopped,
		}

		for _, n := range entry.adHoc.offered {
			rec.Offered = append(rec.Offered, n.ID())
		}

		out = append(out, rec)
	}

	// scope paths are unique, so the sort makes the document
	// deterministic (the ordered-records discipline).
	sort.Slice(out, func(i, j int) bool {
		return out[i].ScopePath < out[j].ScopePath
	})

	return out
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
		rec, err := ledgerRecordOf(ctx, path, e)
		if err != nil {
			return nil, err
		}

		out = append(out, rec)

		folded, err := ledgerRecords(ctx, path, e.folded)
		if err != nil {
			return nil, err
		}

		out = append(out, folded...)
	}

	return out, nil
}

// ledgerRecordOf encodes ONE ledger entry (folded children not
// included — the sweep queue is 1:1 entry→handler run, SRD-082 FR-6).
func ledgerRecordOf(
	ctx context.Context, path string, e *ledgerEntry,
) (checkpoint.LedgerRecord, error) {
	snap, err := checkpoint.EncodeData(ctx, path, e.snapshot)
	if err != nil {
		return checkpoint.LedgerRecord{}, err
	}

	return checkpoint.LedgerRecord{
		ScopePath:       path,
		ActivityID:      e.activityID,
		ActivityName:    e.activityName,
		HandlerID:       e.handlerID,
		HandlerName:     e.handlerName,
		Snapshot:        snap,
		Ordinal:         e.ordinal,
		HandlerEventSub: e.handlerEventSub,
	}, nil
}

// callRecords captures the in-flight Call Activities (SRD-082 FR-7):
// the parent's half of the symmetric link — the awaited child, the
// call node and the parked caller track. The child instance is its own
// record; ls.calls is loop-owned, so the read is a consistent cut.
func (ls *loopState) callRecords() []checkpoint.CallRecord {
	if len(ls.calls) == 0 {
		return nil
	}

	out := make([]checkpoint.CallRecord, 0, len(ls.calls))

	for id, entry := range ls.calls {
		out = append(out, checkpoint.CallRecord{
			ChildID: id,
			NodeID:  entry.node.ID(),
			TrackID: entry.track.ID(),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].ChildID < out[j].ChildID
	})

	return out
}

// sweepRecords captures the resolving compensation sweeps (SRD-082
// FR-6): one record per live handler run — the parked thrower (or the
// Transaction host), the remaining queue in run order, and the entry
// being undone, which RE-RUNS on restore (a handler is an effect;
// at-least-once per ADR-033 §2.3). Everything read is loop-owned.
func (ls *loopState) sweepRecords(
	ctx context.Context,
) ([]checkpoint.SweepRecord, string) {
	if len(ls.sweeps) == 0 {
		return nil, ""
	}

	out := make([]checkpoint.SweepRecord, 0, len(ls.sweeps))

	for _, run := range ls.sweeps {
		rec := checkpoint.SweepRecord{
			ScopePath: string(run.sweep.path),
			Wait:      run.sweep.wait,
		}

		if run.sweep.thrower != nil {
			rec.ThrowerTrack = run.sweep.thrower.ID()
		}

		if run.sweep.txHost != nil {
			rec.TxHostTrack = run.sweep.txHost.ID()
		}

		running, err := ledgerRecordOf(ctx, rec.ScopePath, run.entry)
		if err != nil {
			return nil, "encode: " + err.Error()
		}

		rec.Running = &running

		for _, e := range run.sweep.queue {
			qr, err := ledgerRecordOf(ctx, rec.ScopePath, e)
			if err != nil {
				return nil, "encode: " + err.Error()
			}

			rec.Queue = append(rec.Queue, qr)
		}

		out = append(out, rec)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].ScopePath < out[j].ScopePath
	})

	return out, ""
}

// trackRecord captures one live track; live=false skips it. Every read
// rides the track mutex: the capture runs on the loop goroutine while
// the track's own goroutine may be mid-arming (writes guarded on its
// side too). mirror is the loop-owned iteration position of an
// own-iteration host (SRD-082 FR-2), nil for every other track.
func trackRecord(
	ctx context.Context, t *track, mirror *iterMirror,
) (checkpoint.TrackRecord, bool, error) {
	t.m.RLock()
	defer t.m.RUnlock()

	if !liveTrackStates[t.state] {
		return checkpoint.TrackRecord{}, false, nil
	}

	rec := checkpoint.TrackRecord{
		ID:        t.ID(),
		State:     t.state.String(),
		NodeID:    t.steps[len(t.steps)-1].node.ID(),
		ScopePath: string(t.scopePath),
		ScopeSeg:  t.scopeSeg,
		TaskID:    t.taskID,
		// the routed-activity assignment is set pre-spawn on the loop
		// (spawnAdHoc) and never rewritten, so the read is safe here;
		// restore rebuilds the container's running counts from it
		// (SRD-083 FR-2).
		AdHocActivity: t.adHocActivity,
		Prev:          append([]string{}, t.prev...),
		MsgDefIDs:     append([]string{}, t.msgDefIDs...),
		LoopCounter:   t.loopCounter,
	}

	if t.state == TrackWaitForEvent && !t.timerDeadline.IsZero() {
		rec.Timer = &checkpoint.TimerDescriptor{
			Deadline:   t.timerDeadline,
			CyclesLeft: t.timerCycles,
		}
	}

	// the own-iteration position (SRD-082 FR-2): the mirror exists
	// exactly while the host's iteration is in flight (dropped on the
	// host's end), whatever step-state the host shows between passes.
	// The mirror is loop-owned and staging is loop-written, so both
	// reads are loop-serialized.
	if mirror != nil {
		if err := recordIteration(ctx, &rec, t, mirror); err != nil {
			return checkpoint.TrackRecord{}, false, err
		}
	}

	return rec, true, nil
}

// recordIteration writes the host's iteration position onto its track
// record, as the executor set that replaced the per-instance tracks and
// scopes (SRD-090.A FR-6).
//
// Every mirrored host writes one. `TrackRecord.MI` — the mirror a
// sequential iteration used to ride — is no longer written by anything: it
// survives on the READ side alone, so a schema-5 document captured before
// this slice still restores (FR-7). The one iterated kind that reaches no
// mirror at all is a parallel COMPOSITE Multi-Instance, whose position is
// still the loop-owned group's until M3b retires it.
func recordIteration(
	ctx context.Context, rec *checkpoint.TrackRecord, t *track,
	mirror *iterMirror,
) error {
	var staging json.RawMessage

	if mirror.staging != nil {
		raw, err := checkpoint.EncodeValue(ctx, "track "+t.ID(), mirror.staging)
		if err != nil {
			return err
		}

		staging = raw
	}

	rec.Iteration = &checkpoint.IterationRecord{
		Kind:         mirror.kind,
		N:            mirror.n,
		Completed:    mirror.completed,
		ConditionMet: mirror.conditionMet,
		Staging:      staging,
		Instances:    withTaskIDs(t, mirror.instances),
	}

	return nil
}

// withTaskIDs stamps each instance with the parked-work identity it was
// announced under, so a restore returns it rather than minting a new one
// (ADR-020 §2.12).
//
// Read at CAPTURE rather than posted at launch, because an instance mints its
// id when it parks — after the position is posted, and only if it parks at
// all. Reading here also keeps the loop's mirror describing positions, which
// is what it is for.
func withTaskIDs(
	t *track, insts []checkpoint.IterationInstance,
) []checkpoint.IterationInstance {
	if len(insts) == 0 {
		return insts
	}

	// read-only: the capture runs on the LOOP, and resolving an executor
	// here would store it on a track another goroutine is driving.
	//
	// A RELEASED track has no executor left to ask — and that is exactly the
	// capture whose ids matter, the one a restore reads. It falls back to the
	// set the loop kept when it released the track (track.keepTaskIDs).
	var ids map[int]string

	if owner := t.ownerIfResolved(); owner != nil {
		ids = owner.taskIDSnapshot()
	}

	if len(ids) == 0 {
		ids = t.parkedTaskIDs
	}

	if len(ids) == 0 {
		return insts
	}

	out := make([]checkpoint.IterationInstance, len(insts))
	copy(out, insts)

	for i := range out {
		if id, ok := ids[out[i].Ordinal]; ok {
			out[i].TaskID = id
		}
	}

	return out
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

// persistedStatusOf is the incident-aware persisted status (SRD-079 FR-5,
// ADR-036 §2.5): an in-flight instance with open incidents persists as
// StatusActiveIncidents, so "what needs an operator" is answerable from the
// store without loading payloads. The runtime State vocabulary is untouched —
// the condition is a predicate, not a state (SRD-079 §4.2). Loop goroutine
// only (it reads the loop-owned incident set).
func persistedStatusOf(inst *Instance) repository.Status {
	st := persistedStatus(inst.State())
	if st == repository.StatusActive && inst.openIncidents() > 0 {
		return repository.StatusActiveIncidents
	}

	return st
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
