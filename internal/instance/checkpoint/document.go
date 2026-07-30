package checkpoint

import (
	"encoding/json"
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
const CurrentSchema = 2

// Document is one instance's durable state (SRD-070 FR-3): identity +
// the version pin, status, the scope table, conversation keys, the
// compensation ledger and the live-track table. Everything derivable
// (arming, routing, subscriptions) is rebuilt at restore by
// re-entering the nodes.
type Document struct {
	ConvKeys map[string]string `json:"conv_keys,omitempty"`

	// CompletedBy records who completed each human task — node name → user id
	// (ADR-020 v.2 §2.4.2). It rides the checkpoint because a human task is the
	// wait most likely to dehydrate, so without it the record would vanish exactly
	// in the case it exists for: a LATER node asking who performed an earlier task.
	CompletedBy map[string]string `json:"completed_by,omitempty"`

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

	Schema  int `json:"schema"`
	Version int `json:"version"` // the FR-1 pin
}

// ScopeRecord is one open scope: its path and the codec-encoded data
// committed there.
type ScopeRecord struct {
	Path string          `json:"path"`
	Data json.RawMessage `json:"data,omitempty"`
}

// LedgerRecord is one compensation-ledger entry (ADR-026): the scope it
// belongs to, its completion ordinal, the compensable activity, its
// handler reference and the data snapshot captured at completion.
type LedgerRecord struct {
	ScopePath  string          `json:"scope_path"`
	ActivityID string          `json:"activity_id"`
	HandlerID  string          `json:"handler_id,omitempty"`
	Snapshot   json.RawMessage `json:"snapshot,omitempty"`
	Ordinal    int             `json:"ordinal"`
}

// TrackRecord is one LIVE track: enough to respawn it at its node with
// re-enter semantics (SRD-070 FR-6). Ended/merged tracks are never
// recorded — their effect lives in the committed data.
type TrackRecord struct {
	// Timer is the one wait descriptor of this slice: the recorded
	// absolute deadline overrides re-evaluation at restore (a Duration
	// would otherwise restart — SRD-070 §4.2).
	Timer *TimerDescriptor `json:"timer,omitempty"`

	ID        string   `json:"id"`
	State     string   `json:"state"`
	NodeID    string   `json:"node_id"`
	ScopePath string   `json:"scope_path"`
	ScopeSeg  string   `json:"scope_seg,omitempty"`
	TaskID    string   `json:"task_id,omitempty"`
	Prev      []string `json:"prev,omitempty"`
	MsgDefIDs []string `json:"msg_def_ids,omitempty"`

	LoopCounter int `json:"loop_counter,omitempty"`
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
			errs.D("instance_id", d.InstanceID),
			errs.D("process_id", d.ProcessID))
	}

	d.Schema = CurrentSchema

	raw, err := json.Marshal(d)
	if err != nil {
		return nil, errs.New(
			errs.M("Marshal: document serialization failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.D("instance_id", d.InstanceID),
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
			errs.D("instance_id", d.InstanceID))
	}

	if d.InstanceID == "" || d.ProcessID == "" {
		return nil, errs.New(
			errs.M("Unmarshal: the checkpoint names no instance/process"),
			errs.C(errorClass, errs.InvalidState))
	}

	return &d, nil
}
