package checkpoint

import (
	"encoding/json"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// CurrentSchema is the checkpoint document's wire-schema version
// (ADR-033 §2.1: the document carries it for forward migration; an
// unknown schema fails loud, never a guess).
//
// 1 → 2 (SRD-071 FR-9a) added the armed-boundary set. The bump is ADDITIVE:
// every Schema-1 field means what it always did, and a Schema-1 document
// restores exactly as before — it simply carries no boundaries, which is the
// state the engine was in when it wrote one. So this engine READS an older
// document rather than rejecting it (see Unmarshal); only a document from a
// FUTURE schema is refused, because that one may contain state this engine
// would silently drop.
//
// 2 → 3 (SRD-079 FR-5) added the incident table — same additive rule: a
// Schema-2 document simply carries no incidents, which is the state the
// engine was in when it wrote one.
//
// 3 → 4 (SRD-082 FR-1) added the composite position records — in-flight
// calls, iteration positions, parallel MI open sets, resolving
// compensation sweeps. Additive again: a Schema-3 document was only
// ever written with no construct in flight (the retired capture guards
// guaranteed it), so absent records mean "nothing to rebuild".
//
// 4 → 5 (SRD-083 FR-1) added the Ad-Hoc routing records. Additive with
// one deliberate exception: a Schema ≤ 4 document CAN carry an open
// ad-hoc scope (the construct was never guarded), and such a document
// refuses to restore rather than resuming with the routing state lost
// (SRD-083 FR-6) — loud beats silently wrong.
//
// 5 → 6 (SRD-090.A FR-6) moved an iterated LEAF activity's position into
// the executor set (Iteration). Additive in the wire sense — a Schema-5
// document carries no Iteration — but its READ path is the interesting
// half: a leaf iteration used to persist as a TrackRecord.MI mirror
// (sequential) or as an MIGroupRecord plus one TrackRecord per instance
// (parallel), and neither describes an object that still exists. Both are
// therefore translated on restore into the instances they meant, rather
// than rebuilt as the tracks and scopes they named (FR-7).
// 6 → 7 (SRD-090.A M3c) made the scope table self-describing: a
// ScopeRecord now names the track that opened it and the instance
// ordinal it stands for. Additive in the wire sense — a Schema ≤ 6
// document carries neither — and its read path keeps the derivation
// those documents need: an absent HostTrack falls back to searching the
// restored track table, precedence rule included. New documents are read
// by lookup, which is what lets the search retire once Schema 6 leaves
// support.
const CurrentSchema = 7

// Document is one instance's durable state (SRD-070 FR-3): identity +
// the version pin, status, the scope table, conversation keys, the
// compensation ledger and the live-track table. Everything derivable
// (arming, routing, subscriptions) is rebuilt at restore by
// re-entering the nodes.
type Document struct {
	ConvKeys map[string]string `json:"conv_keys,omitempty"`

	// StartedAt is when the instance originally began, in RFC 3339. It rides the
	// checkpoint because a hydrated instance is the SAME logical instance: without
	// it, a rebuild silently restamps the start time to "now", so RUNTIME/STARTED_AT
	// reports the age of the latest rebuild rather than of the process, and every
	// "how long has this been running" answer is wrong by exactly the time that
	// mattered — the long wait that caused the dehydration in the first place.
	//
	// Empty on a checkpoint written before this field existed; a restore then keeps
	// whatever the rebuild set, which is the old behavior.
	StartedAt string `json:"started_at,omitempty"`

	// CompletedBy records who completed each human task — node name → user id
	// (ADR-020 v.2 §2.4.2). It rides the checkpoint because a human task is the
	// wait most likely to dehydrate, so without it the record would vanish exactly
	// in the case it exists for: a LATER node asking who performed an earlier task.
	CompletedBy map[string]string `json:"completed_by,omitempty"`

	// IterationOwners records who completed each INSTANCE of an iterated
	// activity — activity id → (ordinal → user id) (ADR-025 §2.15,
	// SRD-090.D FR-4). It rides the checkpoint for CompletedBy's reason, and
	// more so: a fan-out over human work exists because N approvals take
	// days, so dehydration is its ordinary state rather than an edge one. A
	// register rebuilt empty would answer "nobody did any of it" for exactly
	// the workload it was built for.
	//
	// CompletedBy cannot stand in for it: that keys by node, so an iterated
	// activity has one entry however many instances ran.
	//
	// Empty on a checkpoint written before this field existed, which restores
	// as it did then: no answer rather than a wrong one.
	IterationOwners map[string]map[string]string `json:"iteration_owners,omitempty"`

	// Iterations records what each iterated activity DID — activity id →
	// its account (ADR-025 §2.9.2, SRD-090.D FR-4). It rides the checkpoint
	// for IterationOwners' reason: the register answers a question asked
	// AFTER the activity finished, by a node that may well be running in a
	// rebuilt instance, and one rebuilt empty would report an activity that
	// processed three items as having processed none.
	//
	// It is not TrackRecord.Iteration, which is a live activity's POSITION —
	// where a fan-out has got to, so it can resume. This is the account it
	// leaves behind, and it outlives the track that produced it.
	Iterations map[string]ActivityIteration `json:"iterations,omitempty"`

	InstanceID string `json:"instance_id"`
	// ParentID/CallNodeID record child linkage informationally (a child
	// instance is its own record; re-linking a live call is SRD-071+).
	ParentID   string `json:"parent_id,omitempty"`
	CallNodeID string `json:"call_node_id,omitempty"`
	ProcessID  string `json:"process_id"`
	Status     string `json:"status"`

	Scopes  []ScopeRecord  `json:"scopes"`
	Ledgers []LedgerRecord `json:"ledgers,omitempty"`
	Tracks  []TrackRecord  `json:"tracks"`
	// Boundaries are the boundary events armed over the captured tracks
	// (Schema 2, SRD-071 FR-9a). Absent in a Schema-1 document, which is
	// why the field is optional rather than a migration.
	Boundaries []BoundaryRecord `json:"boundaries,omitempty"`
	// Incidents is the incident table (Schema 3, SRD-079 FR-5) — open AND
	// closed: a dead-lettered incident's record is the durable dead letter
	// (ADR-036 §2.5), so closing never removes it.
	Incidents []IncidentRecord `json:"incidents,omitempty"`
	// Calls are the in-flight Call Activities (Schema 4, SRD-082 FR-1,
	// ADR-033 v.4 §2.1 item 7): the awaited child instance, the call
	// node and the parked caller track. The child is its own record —
	// this is the parent's half of the symmetric link (§2.10).
	Calls []CallRecord `json:"calls,omitempty"`
	// MIGroups are the parallel multi-instance open sets (Schema 4,
	// SRD-082 FR-1): which per-instance scopes are still open, at which
	// ordinals, with the outputs collected so far.
	//
	// READ ONLY from Schema 6 (SRD-090.A FR-6/FR-7). Nothing writes it:
	// a fan-out's position is its host's Iteration record, and the open
	// instances are their own scopes — whose ordinals are in their paths,
	// so the set is derived rather than stored. It stays so a document
	// written before that still restores; the restore translates it.
	MIGroups []MIGroupRecord `json:"mi_groups,omitempty"`
	// Sweeps are the resolving compensation throws (Schema 4, SRD-082
	// FR-1): the remaining queue and the entry being undone — the
	// ledger alone is not the state once a sweep has consumed from it.
	Sweeps []SweepRecord `json:"sweeps,omitempty"`
	// AdHoc are the open Ad-Hoc containers' routing states (Schema 5,
	// SRD-083 FR-1): completed counts, a manual container's pending
	// offer, and the stopped flag. Running counts are NOT recorded —
	// they derive from the track table's AdHocActivity assignments
	// (ADR-033 §2.1 minimality).
	AdHoc []AdHocRecord `json:"adhoc,omitempty"`

	Schema  int `json:"schema"`
	Version int `json:"version"` // the FR-1 pin
}

// IncidentRecord is one durable incident (ADR-036 §2.1, SRD-079 §3.3): the
// failure the model did not handle, with everything a future attempt needs —
// the node, the scope binding, the lineage of the failed track, the cause,
// the attempt history and the failure-time data snapshot.
type IncidentRecord struct {
	FirstAt time.Time `json:"first_at"`
	LastAt  time.Time `json:"last_at"`
	// RetryAt is the scheduled policy-retry deadline; absent when the
	// incident waits for an operator (the retry slice re-arms it at restore).
	RetryAt    *time.Time      `json:"retry_at,omitempty"`
	ID         string          `json:"id"`
	NodeID     string          `json:"node_id"`
	TrackID    string          `json:"track_id"`
	NodeName   string          `json:"node_name,omitempty"`
	ScopePath  string          `json:"scope_path"`
	ScopeSeg   string          `json:"scope_seg,omitempty"`
	Cause      string          `json:"cause"`
	CauseClass string          `json:"cause_class,omitempty"`
	State      string          `json:"state"`
	Prev       []string        `json:"prev,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	Attempts   int             `json:"attempts"`
}

// ScopeRecord is one open scope: its path, the codec-encoded data
// committed there, and — from Schema 7 — WHO opened it.
//
// HostTrack and Ordinal make the scope table self-describing (SRD-090.A
// M3c). Before them, restore reconstructed both by searching the track
// table for a track whose composite node derives this path, and read the
// instance ordinal out of the path's own segment. That search needs a
// precedence rule, because `sp-a-1` is BOTH instance 1 of node `a` and the
// own scope of a node named `a-1`, and a path built to be read by a human
// cannot distinguish them. Recording the host resolves it by lookup
// instead — a track id survives a restore unchanged, so the answer is
// exact rather than inferred.
//
// Ordinal is meaningful only when HostTrack is set: -1 for a host's own
// scope (a plain composite, or a sequential pass reusing one scope), and
// the 0-based instance number for one of N fanned out. An empty HostTrack
// marks a Schema ≤ 6 document, whose restore falls back to the search.
type ScopeRecord struct {
	Path      string          `json:"path"`
	HostTrack string          `json:"host_track,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Ordinal   int             `json:"ordinal,omitempty"`
}

// LedgerRecord is one compensation-ledger entry (ADR-026): the scope it
// belongs to, its completion ordinal, the compensable activity, its
// handler reference and the data snapshot captured at completion.
// HandlerEventSub picks the restore-side seed mode (Schema 4, SRD-082
// FR-6): an Event Sub-Process handler seeds its child scope, a
// boundary handler seeds its frame inputs — a restored sweep that
// guessed wrong would mis-seed. The names ride for observability.
type LedgerRecord struct {
	ScopePath       string          `json:"scope_path"`
	ActivityID      string          `json:"activity_id"`
	ActivityName    string          `json:"activity_name,omitempty"`
	HandlerID       string          `json:"handler_id,omitempty"`
	HandlerName     string          `json:"handler_name,omitempty"`
	Snapshot        json.RawMessage `json:"snapshot,omitempty"`
	Ordinal         int             `json:"ordinal"`
	HandlerEventSub bool            `json:"handler_event_sub,omitempty"`
}

// TrackRecord is one LIVE track: enough to respawn it at its node with
// re-enter semantics (SRD-070 FR-6). Ended/merged tracks are never
// recorded — their effect lives in the committed data.
type TrackRecord struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	NodeID    string `json:"node_id"`
	ScopePath string `json:"scope_path"`
	ScopeSeg  string `json:"scope_seg,omitempty"`
	TaskID    string `json:"task_id,omitempty"`

	// Timer is the one wait descriptor of this slice: the recorded
	// absolute deadline overrides re-evaluation at restore (a Duration
	// would otherwise restart — SRD-070 §4.2).
	Timer *TimerDescriptor `json:"timer,omitempty"`
	// MI is the sequential-iteration position (Schema 4, SRD-082 FR-1)
	// of a COMPOSITE host that drives its own passes — sequential MI or
	// a Standard Loop. nil for every other track. A leaf activity's
	// iteration moved to Iteration in Schema 6; this stays for the
	// composite kinds until they follow (SRD-090.A M3), and is still
	// READ from a Schema-5 document whatever the kind.
	MI *MIRecord `json:"mi,omitempty"`

	// Iteration is an iterated LEAF activity's executor set (Schema 6,
	// SRD-090.A FR-6): the instances that are live, keyed by ordinal.
	// It replaces both halves of the old shape — the MI mirror a
	// sequential leaf rode, and the MIGroupRecord plus per-instance
	// TrackRecords a parallel leaf rode — because a leaf instance is no
	// longer a track and opens no scope of its own.
	Iteration *IterationRecord `json:"iteration,omitempty"`
	// AdHocActivity names the inner activity this track was routed to
	// inside an Ad-Hoc container (Schema 5, SRD-083 FR-2) — the
	// track-table half of the AdHocRecord: restore rebuilds the
	// container's running counts from these. Empty for every other
	// track.
	AdHocActivity string `json:"adhoc_activity,omitempty"`

	Prev      []string `json:"prev,omitempty"`
	MsgDefIDs []string `json:"msg_def_ids,omitempty"`

	LoopCounter int `json:"loop_counter,omitempty"`
}

// CallRecord is one in-flight Call Activity (Schema 4, SRD-082 FR-1):
// the parent's half of the parent↔child link (ADR-033 v.4 §2.10).
type CallRecord struct {
	ChildID string `json:"child_id"`
	NodeID  string `json:"node_id"`
	TrackID string `json:"track_id"`
}

// MIRecord is a sequential MI / Standard Loop position: passes fully
// completed, the frozen instance count (0 for a Standard Loop, whose
// bound is the loop condition), the outputs collected so far (a
// canonical-codec array) and whether the completionCondition already
// fired. Names (inputItem, outputRef, …) derive from the node and are
// never stored.
type MIRecord struct {
	Staging      json.RawMessage `json:"staging,omitempty"`
	N            int             `json:"n,omitempty"`
	Completed    int             `json:"completed"`
	ConditionMet bool            `json:"condition_met,omitempty"`
}

// IterationInstance is ONE live instance of an iterated activity (Schema
// 6, SRD-090.A FR-6): its 0-based ordinal — the join key across the
// record, the token projection and an incident — and what it is doing.
// ChildID names the callee a call executor owns, and is what lets a
// recovered caller bind a completing child's output to the slot it
// belongs in; empty for every other kind.
//
// Frames are deliberately absent: the split item is the collection
// element at the ordinal and the counter IS the ordinal, both recomputed
// (ADR-025 §2.4 fixes cardinality once, so the collection cannot
// shift underneath).
type IterationInstance struct {
	// Eligible is the verdict this iteration's announcement RESOLVED — who
	// may act on its task (ADR-020 §2.7).
	//
	// It rides the checkpoint because eligibility is assessed ONCE, at the
	// announcement, in the data context of the iteration being announced. A
	// restore cannot recompute it: the element the iteration was seeded with
	// is frame-local to an execution that no longer exists, so a re-resolution
	// reads the host's scope instead and a performer expression naming "the
	// reviewer this one is for" resolves to nobody — locking every holder out
	// of the task their inbox is still showing them.
	//
	// Optional: a document written before this field restores as it always
	// did, resolving at the host's scope, which is all it ever recorded.
	Eligible *TaskEligibility `json:"eligible,omitempty"`

	State   string `json:"state"` // running | waiting | completed
	ChildID string `json:"child_id,omitempty"`

	// TaskID is this instance's parked-work identity, when it is waiting on
	// a capability rather than an event — a human task, an external worker
	// (ADR-020 §2.12).
	//
	// Recorded per INSTANCE because the identity is per instance: a fan-out
	// holds N of them, and the track's single slot can carry only one. The id
	// outlives the instance's residency in the distributor's inbox, so a
	// restore that minted fresh ones would invalidate every reference a
	// person or a UI is holding — the same rule SRD-071 FR-8 states for a
	// lone task, at iteration granularity.
	TaskID string `json:"task_id,omitempty"`

	Ordinal int `json:"ordinal"`
}

// ActivityIteration is what one iterated activity DID: the shape it ran in,
// how many instances it froze at, and how they ended. The durable half of
// BPMN's §2.9 counts, which end with the activation they describe.
//
// Not to be read as IterationRecord below, which is a live activity's
// POSITION — where it has got to, so it can resume. This is the account it
// leaves behind, and it outlives the track that produced it.
type ActivityIteration struct {
	Kind       string `json:"kind"`
	Total      int    `json:"total"`
	Completed  int    `json:"completed"`
	Terminated int    `json:"terminated"`
}

// TaskEligibility is a resolved assignment triad, as the announcement froze it
// (ADR-020 §2.7). Identifier sets only — the expressions that produced them are
// the model's and are not persisted.
type TaskEligibility struct {
	Assignee        []string `json:"assignee,omitempty"`
	CandidateUsers  []string `json:"candidate_users,omitempty"`
	CandidateGroups []string `json:"candidate_groups,omitempty"`
	Roles           []string `json:"roles,omitempty"`

	// the Declared flags: a slot resolving to nobody and a slot the task does
	// not carry authorize differently, so which it was has to survive.
	AssigneeDeclared        bool `json:"assignee_declared,omitempty"`
	CandidateUsersDeclared  bool `json:"candidate_users_declared,omitempty"`
	CandidateGroupsDeclared bool `json:"candidate_groups_declared,omitempty"`
	RolesDeclared           bool `json:"roles_declared,omitempty"`
}

// IterationRecord is an iterated activity's live instances (Schema 6,
// SRD-090.A FR-6) — the executor set that replaces per-iteration
// TrackRecords and the TrackRecord.MI mirror. Kind names the iteration
// shape, N the count frozen at activation (0 for a Standard Loop, whose
// bound is its condition), Completed the instances fully done, Staging
// their assembled outputs, and ConditionMet whether the
// completionCondition already fired.
type IterationRecord struct {
	Kind         string              `json:"kind"` // loop | mi_sequential | mi_parallel
	Staging      json.RawMessage     `json:"staging,omitempty"`
	Instances    []IterationInstance `json:"instances,omitempty"`
	N            int                 `json:"n,omitempty"`
	Completed    int                 `json:"completed"`
	ConditionMet bool                `json:"condition_met,omitempty"`
}

// OpenScope is one still-open per-instance scope of a parallel MI
// group: its path (the scope's data rides the Scopes table) and its
// 0-based instance ordinal — the one position not derivable from the
// track table.
type OpenScope struct {
	Path    string `json:"path"`
	Ordinal int    `json:"ordinal"`
}

// MIGroupRecord is a parallel multi-instance group mid-fan-out
// (Schema 4, SRD-082 FR-1): the host, the frozen N, the open set and
// the collected outputs. Completed instances stay completed — their
// outputs are in Staging; terminated counts are computed at cancel
// time and never stored.
//
// Written by Schema 4 and 5 only — see Document.MIGroups.
type MIGroupRecord struct {
	HostTrack string          `json:"host_track"`
	Staging   json.RawMessage `json:"staging,omitempty"`
	Open      []OpenScope     `json:"open"`
	N         int             `json:"n"`
	Pending   int             `json:"pending,omitempty"`
}

// SweepRecord is a resolving compensation throw (Schema 4, SRD-082
// FR-1): the parked thrower, the Transaction host when the sweep
// drives an abort, the scope it collects for, the REMAINING queue in
// run order and the entry being undone — which re-runs on restore
// (a handler is an effect; at-least-once per ADR-033 §2.3).
type SweepRecord struct {
	ThrowerTrack string         `json:"thrower_track"`
	TxHostTrack  string         `json:"tx_host_track,omitempty"`
	ScopePath    string         `json:"scope_path"`
	Running      *LedgerRecord  `json:"running,omitempty"`
	Queue        []LedgerRecord `json:"queue,omitempty"`
	Wait         bool           `json:"wait,omitempty"`
}

// AdHocRecord is one open Ad-Hoc container's routing state (Schema 5,
// SRD-083 FR-2): the parked host, the container's scope, how many
// times each inner activity has settled, the candidates a manual
// container is holding for its host, and whether the completion
// condition already fired (with the reason routing stopped). Live
// work is NOT here — each routed track records its AdHocActivity, so
// the running counts rebuild from the track table.
type AdHocRecord struct {
	Completed  map[string]int `json:"completed,omitempty"`
	HostTrack  string         `json:"host_track"`
	ScopePath  string         `json:"scope_path"`
	StopReason string         `json:"stop_reason,omitempty"`
	Offered    []string       `json:"offered,omitempty"`
	Stopped    bool           `json:"stopped,omitempty"`
}

// BoundaryRecord is one ARMED boundary event guarding a captured track
// (SRD-071 FR-9a). A boundary is not a track — it has no token and no
// lineage — so it rides its host's record set rather than becoming one.
//
// Re-arming reconstructs everything about a boundary from the model EXCEPT
// when it fires: a duration-based timer re-evaluated at restore would restart
// its clock, so the resolved deadline is what this record exists to carry.
// Non-timer boundaries are recorded too, with no Timer: they restore nothing,
// but the set states what was armed, which is what "release only when every
// armed boundary is held" has to consult.
// DefIndex, not the definition's id: ids are minted per model build, so a
// recovering engine's model carries different ones and a recorded id would
// never resolve. Definition order within a boundary is model order.
type BoundaryRecord struct {
	Timer      *TimerDescriptor `json:"timer,omitempty"`
	HostTrack  string           `json:"host_track"`
	BoundaryID string           `json:"boundary_id"`
	DefIndex   int              `json:"def_index"`
}

// TimerDescriptor pins a parked timer wait.
type TimerDescriptor struct {
	Deadline   time.Time `json:"deadline"`
	CyclesLeft int       `json:"cycles_left,omitempty"`
}

// Marshal serializes the document, stamping the current schema.
func (d *Document) Marshal() ([]byte, error) {
	if d == nil {
		return nil, errs.New(
			errs.M("Marshal: a nil Document isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if d.InstanceID == "" || d.ProcessID == "" {
		return nil, errs.New(
			errs.M("Marshal: a Document needs instance and process ids"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D(observability.AttrInstanceID, d.InstanceID),
			errs.D(observability.AttrProcessID, d.ProcessID))
	}

	d.Schema = CurrentSchema

	raw, err := json.Marshal(d)
	if err != nil {
		return nil, errs.New(
			errs.M("Marshal: document serialization failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D(observability.AttrInstanceID, d.InstanceID),
			errs.E(err))
	}

	return raw, nil
}

// Unmarshal parses a checkpoint payload, refusing unknown schemas loud
// — the forward-migration gate.
func Unmarshal(payload []byte) (*Document, error) {
	if len(payload) == 0 {
		return nil, errs.New(
			errs.M("Unmarshal: an empty checkpoint payload isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	var d Document
	if err := json.Unmarshal(payload, &d); err != nil {
		return nil, errs.New(
			errs.M("Unmarshal: checkpoint payload doesn't parse"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	// An OLDER schema reads: every bump so far has been additive, so an old
	// document is a new one with fields absent, and refusing it would strand
	// every instance written before the engine was upgraded. A NEWER one is
	// refused — it may carry state this engine does not know to restore, and
	// silently dropping durable state is worse than failing loud. A zero
	// schema is not "old", it is a document that never declared one.
	if d.Schema < 1 || d.Schema > CurrentSchema {
		return nil, errs.New(
			errs.M("Unmarshal: unsupported checkpoint schema %d "+
				"(this engine reads schema 1..%d)", d.Schema, CurrentSchema),
			errs.C(errorClass, errs.InvalidState),
			errs.D(observability.AttrInstanceID, d.InstanceID))
	}

	if d.InstanceID == "" || d.ProcessID == "" {
		return nil, errs.New(
			errs.M("Unmarshal: the checkpoint names no instance/process"),
			errs.C(errorClass, errs.InvalidState))
	}

	return &d, nil
}
