# SRD-082 — Checkpoint fidelity for composite constructs and durable Call Activity children

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-08-06 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-033 v.4](../design/ADR-033-persistence-and-state.md) §2.1 (items 5–7), §2.2 (re-enter applies to steps, not recorded composites), §2.10 (composite fidelity; durable, symmetrically linked children), [ADR-023 v.3](../design/ADR-023-sub-process-and-call-activity.md) §2.7 (the restart contract) |
| Upstream | [ADR-025 v.2](../design/ADR-025-activity-iteration-loop-and-multi-instance.md) §2.12 (the iteration decorator whose position this persists), [ADR-026 v.1](../design/ADR-026-compensation-events.md) §2.1/§2.4 (the ledger and the sequential sweep) |
| Related | [SRD-070](SRD-070-instance-checkpoint-and-restart-recovery.md) (the capture/restore machinery this grows; its FR-4 deferral posture retires here), [SRD-071 v.2.5](SRD-071-instance-dehydration-and-wake-on-trigger.md) (the wake/continuation paths), [SRD-079](SRD-079-incidents-and-retry.md) (schema 3, which this bumps to 4) |
| Tracking | #277 |

An instance can today be checkpointed in a state it cannot be
faithfully restored into — or not checkpointed at all while a
construct is in flight. This SRD retires both failure modes: every
composite construct records its position in the checkpoint document
(schema 4), restore rebuilds it **at that position**, and Call
Activity children become durable instances re-linked to their parent
on recovery. The issue names three constructs; the evidence below
shows the silent half of the problem is in three **unguarded**
siblings, which land here too ("no pre-existing errors").

## §1 Background (verified)

- **The deferral guards** — one switch in `captureDocument`
  (`internal/instance/checkpoint_capture.go:138-145`): non-empty
  `ls.calls`, `ls.miGroups` or `ls.sweeps` → no write, a
  `PhaseCheckpointDeferred` fact at Warn
  (`checkpoint_capture.go:339-349`). The mirror guard blocks
  dehydration for the same three maps (`loop.go:1131-1134`). This was
  SRD-070 FR-4's explicit stopgap ("Full-fidelity capture of those
  constructs is SRD-071+ territory").
- **The guard set is narrower than the gap.** Sequential MI and
  Standard Loop keep their position in `miState{collection, staging,
  inputItem, outputRef, outputItem, numberOfInstances, completed}` — a
  **track field owned by the runner goroutine** (`mi.go:62-71`), which
  no guard inspects; `evScopeOpen` is a persist point
  (`checkpoint_capture.go:42`), so a sequential MI **checkpoints
  mid-iteration today** and restores with the iteration restarted at
  pass 0 while the in-flight pass's inner tracks also respawn. Only
  `TrackRecord.LoopCounter` survives (`checkpoint_capture.go:287`).
- **Composite scope entries are never restored.** `Restore` rebuilds
  scopes' *data*, keys, ledgers, tracks, incidents — and nothing else
  (`internal/instance/restore.go:75-131`); `ls.scopes` entries
  (`scopeEntry{host, node, parent, active, ordinal, aborting}`,
  `scope_runtime.go:74-95`) are absent after restore, so `decScope` /
  `decScopePinned` early-return on the missing key
  (`scope_runtime.go:355-359`, `:337-341`), a
  drained child scope never resumes its host, and the host track
  (state `TrackExecutingStep`, in `liveTrackStates`) re-enters the
  composite from the top — **double-executing the body**.
- **The sweep guard has a window.** `applyCompensate` consumes ledger
  entries out of `ls.ledgers` (`compensation_watch.go:107-137`)
  **before** the first `runNextCompensation` registers the sweep
  (`compensation_watch.go:216`); a capture in that window (or the
  zero-queue `finishSweep` path, `compensation_watch.go:88-93`)
  persists a ledger already drained — compensability silently lost.
  Mid-sweep state is `compSweep{thrower, txHost, path, queue, wait}` +
  the running handler's `sweepRun{sweep, entry}`
  (`compensation_watch.go:47-54`, `:260-263`).
- **A Call Activity child is not persisted at all.**
  `WithCheckpointing` is applied at exactly three sites —
  `instanceOptions` (`pkg/thresher/thresher.go:1320`), recovery
  (`recovery.go:97`), wake (`wake.go:136`) — and
  `Thresher.InvokeProcess` uses none of them
  (`pkg/thresher/invoker.go:65-68`), despite `instanceOptions`' doc
  comment claiming it covers "a Call Activity child"
  (`thresher.go:1301-1311`). After a crash the child is gone and
  nothing in the store names it. The parent side is
  `loopState.calls map[string]*callEntry{track, node, child}` keyed by
  child instance id (`loop.go:72-77`, `calls.go:33-39`), completion
  via a watcher on `child.Done()` (`calls.go:113-126`); on the
  restore side `Document.ParentID/CallNodeID` are informational only
  (`checkpoint/document.go:54-57`) — `Restore` re-applies
  `withCallLinkage` for facts (`restore.go:99-101`) and nothing
  rebuilds `ls.calls`.
- **The document** is at `CurrentSchema = 3`
  (`internal/instance/checkpoint/document.go:26`; 1→2 boundaries
  SRD-071 FR-9a, 2→3 incidents SRD-079 FR-5); restore reads older
  schemas and refuses future ones loud (`document.go:219-225`).
- **The re-link substrate exists**: `Thresher.settledFor(id)` mints
  the per-instance settled channel on demand (`locked.go:250-258`) —
  order-independent, so a restored parent can await a child that has
  not itself been recovered yet; recovery lists both records in the
  same group scan (`recovery.go:24`). Today `InvokeProcess` wires a
  **local** settled channel instead (`invoker.go:63-67`,
  `settled := make(chan struct{})`) — unregistered under the child's
  id, so nothing could re-find it after a restart; FR-7 moves the
  child onto the registry channel.

## §2 Requirements

### §2.1 Functional

- **FR-1 — schema 4: the position records.** `checkpoint.Document`
  grows, additively (schema 3 documents still restore):
  - `Calls []CallRecord{ChildID, NodeID, TrackID string}` — one per
    in-flight Call Activity (ADR-033 v.4 §2.1 item 7).
  - `TrackRecord` gains `MI *MIRecord{N, Completed int, Staging
    json.RawMessage, ConditionMet bool}` — the sequential MI /
    Standard Loop position attached to its host track (item 5;
    `Staging` is the collected-outputs array in the canonical value
    encoding; names like `inputItem` are derived from the node, never
    stored).
  - `MIGroups []MIGroupRecord{HostTrack string, N, Pending int,
    Staging json.RawMessage, Open []OpenScope{Path string, Ordinal
    int}}` — the parallel group's open set (item 5).
  - `Sweeps []SweepRecord{ThrowerTrack, TxHostTrack string, ScopePath
    string, Wait bool, Queue []LedgerRecord, Running *LedgerRecord}` —
    the resolving compensation's remaining queue and the entry being
    undone (item 6).
  - `CurrentSchema` 3 → 4; `Marshal` stamps 4; the future-schema
    refusal is untouched.
- **FR-2 — the loop mirrors off-loop iteration position.** The
  decorator protocol already round-trips every pass through the loop
  (`scopeRequest{op: scopeOpen|scopeFanOut|scopeReArm|scopeComplete}`,
  `scope_decorator.go:47-55`); the loop records what the protocol
  shows it — per-host iteration position in loop-owned state — so
  capture reads a consistent cut without touching the runner's
  goroutine-owned `miState` (ADR-025 §2.12's "the decorator must not
  mutate loop state" holds; the loop observing the protocol is not
  the decorator mutating). The runner remains authoritative at
  runtime; the mirror exists for capture only.
- **FR-3 — sequential MI and Standard Loop restore at position.** A
  restored host track carrying `MI` re-enters its composite with the
  decorator **seeded**: completed passes are not re-run, `staging`
  returns the collected outputs, a fired `completionCondition` is not
  re-evaluated backwards, and the in-flight pass restarts from its
  restored scope data (re-enter applies to the pass, not the
  construct — ADR-033 v.4 §2.2). The §2.9 counters
  (`numberOfInstances`, `loopCounter`, …) re-publish from the record.
- **FR-4 — parallel MI restores its open set.** A restored
  `MIGroupRecord` rebuilds `ls.miGroups[host]`: the still-open
  per-instance scopes re-open at their recorded ordinals over their
  restored scope data, their inner tracks respawn (they are in the
  track table), the runner re-arms awaiting the remaining drains, and
  completed instances stay completed (their outputs are in
  `Staging`). `numberOfTerminatedInstances` remains a cancel-time
  computation (never stored — unchanged).
- **FR-5 — composite scope entries rebuild (the double-execution
  fix).** Restore derives `ls.scopes` from what the document already
  carries — for every open non-root `ScopeRecord` path, the host
  track (the `TrackExecutingStep` track whose node hosts that scope),
  the parent path, and the live-inner-track count from the restored
  track table — so a drained scope resumes its host **exactly once**
  and the host never re-enters a composite whose body is mid-flight.
  Pure derivation: no new document field (ADR-033 §2.1's minimality).
- **FR-6 — the sweep is captured and restored; the window closes.**
  Capture writes `SweepRecord`s from `ls.sweeps` + the active
  `sweepRun`; restore rebuilds the sweep — the thrower re-parks, the
  remaining queue (including a `Running` entry, which **re-runs**: a
  handler is at-least-once per ADR-033 §2.3) resumes in order, a
  `TxHostTrack` sweep re-drives `finalizeTransaction` on drain. The
  drained-ledger window closes structurally: entries consumed into a
  sweep are always either in `ls.ledgers` or in a captured
  `SweepRecord` — `applyCompensate` registers the sweep (and its
  queue) **before** removing entries from the ledger maps.
- **FR-7 — Call Activity children are durable and re-linked.**
  `Thresher.InvokeProcess` applies the same checkpointing options as
  every other launch site (fixing the `instanceOptions` comment's
  claim); the child's record carries its own lease/group plus
  `ParentID`/`CallNodeID` (already in the document); the parent's
  capture writes `CallRecord`s from `ls.calls`. On recovery both
  records list in the same group scan; the restored parent rebuilds
  `ls.calls` and re-establishes the completion watch through the
  engine (`settledFor(childID)` is mint-on-demand, so parent/child
  recovery order is irrelevant); the recovered child runs under the
  normal claim discipline and its terminal outcome re-enters the
  restored parent. **Loud failure modes** (ADR-033 v.4 §2.10): a
  restored parent whose `CallRecord.ChildID` has no repository record
  → that parent's restore fails (the per-instance recovery failure);
  a recovered child whose `ParentID` record is absent → fails loud,
  never runs orphaned. The cancel cascade (`cleanupCall`,
  `calls.go:249-257`) works on the re-linked pair unchanged.
  **Discovery separates roots from called children**: today
  `Thresher.Instances(filter)` (`pkg/thresher/discovery.go:30`,
  SRD-019) lists both indistinguishably. The registry records the
  parent linkage; `Instances` gains `InstancesRoots` /
  `InstancesChildren` filters (existing filters keep their meaning),
  and the `InstanceHandle` exposes `ParentID()`/`CallNodeID()` — a
  host listing "processes" shows roots only, with children reachable
  through their parent. (Multi-Instance iterations are scopes inside
  ONE instance and never appear in this registry — the separation
  concerns Call Activity children only.)
- **FR-8 — the capture guards retire per milestone; the dehydration
  guard stays, re-justified.** `captureDocument`'s three-construct
  switch retires one case per milestone, exactly when the construct's
  position becomes part of the document — the guard exists precisely
  while the construct is uncapturable (parallel MI in M2, the sweep in
  M3, the call in M4). After M4 capture always writes (deferral
  remains only for the encode/save error paths, still loud). The dehydration gate
  (`loop.go:1131-1134`) **keeps refusing** while these constructs are
  in flight, for the true reason now recorded in its comment: an
  in-flight construct is *active work* (a running child, a running
  handler, an iterating body), not a passive wait — releasing the
  goroutines would strand it. Dehydrating a parent parked solely on a
  durable call is future work, out of scope here.
- **FR-9 — schema-3 compatibility.** A schema-3 document (no
  position records) restores exactly as today — such a document was
  only ever written with no construct in flight (the old guards
  guaranteed it), so absent records mean "nothing to rebuild", not
  data loss. A fixture-based test proves it.

### §2.2 Non-functional

- **NFR-1** — no new dependencies; all new state serializes through
  the existing canonical value/JSON encodings.
- **NFR-2** — every restore-side failure is loud and classified (the
  errs idiom); no silent construct drop remains anywhere in
  capture/restore.
- **NFR-3** — `make ci` green; diff-coverage ≥95% (aim 100%);
  touched functions ≥80%.

## §3 Models

```go
// internal/instance/checkpoint — schema 4 (additive over 3)
const CurrentSchema = 4

type CallRecord struct {
	ChildID string // the awaited child instance
	NodeID  string // the Call Activity node
	TrackID string // the parked caller track
}

type MIRecord struct { // sequential MI / Standard Loop, on its host track
	Staging      json.RawMessage // collected outputs (canonical array)
	N            int             // frozen numberOfInstances (0 for Standard Loop)
	Completed    int             // passes fully completed
	ConditionMet bool            // completionCondition already fired
}

type OpenScope struct {
	Path    string // the per-instance scope path (its data is in Scopes)
	Ordinal int    // the 0-based instance ordinal
}

type MIGroupRecord struct { // parallel MI
	HostTrack string
	Staging   json.RawMessage
	Open      []OpenScope
	N         int
	Pending   int
}

type SweepRecord struct { // a resolving compensation throw
	ThrowerTrack string
	TxHostTrack  string // "" unless a Transaction abort drives it
	ScopePath    string
	Queue        []LedgerRecord // remaining, in run order
	Running      *LedgerRecord  // the entry being undone (re-runs on restore)
	Wait         bool
}

type Document struct {
	// … unchanged fields …
	Calls    []CallRecord
	MIGroups []MIGroupRecord
	Sweeps   []SweepRecord
}

type TrackRecord struct {
	// … unchanged fields …
	MI *MIRecord
}
```

Worked trace (T-8's shape): a process with a Call Activity inside a
sequential 3-instance MI parks mid-pass-2 while the pass's body waits
on a called child; the checkpoint (schema 4) carries the MI position
(`Completed: 1`, staging with pass-1's output), the call record, the
child's own record exists beside it. The engine dies. Recovery
restores the child (it resumes its own timer wait) and the parent: the
MI decorator seeds at pass 2, the pass's track re-parks on the call,
`ls.calls` re-links via the minted settled channel. The child
completes → outputs bind → pass 2 completes → pass 3 runs → the MI
assembles all three outputs → the instance completes. Nothing
re-executed pass 1; no second child was launched.

## §4 Analysis & decisions

- **§4.1 Faithful capture for all five constructs; refusal only for
  the unserializable.** Decided at ADR level (ADR-033 v.4 §2.10). The
  per-construct outcome the issue asked to record: **restore
  faithfully** for composite scopes, sequential/parallel MI, Standard
  Loop, the compensation sweep, and the Call Activity (via child
  durability). "Fail loudly at capture" was weighed per construct and
  rejected as the steady state for all five: the deferral does not
  compose (a construct-dense process may never find a capturable
  instant, silently widening the loss window), and two of the five
  were never guarded at all — the "refuse" posture demonstrably leaks.
- **§4.2 The loop mirrors the decorator's position — the runner stays
  authoritative.** The alternative (capture reads `miState` directly)
  races the runner goroutine; the alternative (move iteration state
  wholly into the loop) reverts ADR-025 §2.12's off-loop decision.
  The mirror is the minimal consistent-cut-preserving shape: the loop
  records only what the existing protocol already shows it.
- **§4.3 A `Running` sweep entry re-runs.** A compensation handler is
  an effect; ADR-033 §2.3's at-least-once posture covers it (handlers
  read an immutable snapshot, ADR-026 §2.5, so a re-run is
  well-defined). Recording sub-handler progress would be mid-step
  state, which §2.2 forbids.
- **§4.4 Child re-link through the settled registry, not a new seam.**
  `settledFor` already mints per-instance channels on demand and
  `watchCall` already consumes a `Done()` signal; recovery re-links by
  reconnecting those existing pieces. The rejected alternative — a
  dedicated `ReattachProcess` API on the invoker seam — adds public
  surface for what is engine-internal recovery wiring.
- **§4.5 Scope entries are derived, not stored.** Host track, parent
  path and live-inner count are all recoverable from the document's
  existing track table + scope paths; storing `scopeEntry` would
  duplicate derivable state against ADR-033 §2.1's minimality rule.
  MI ordinals are NOT derivable (the open set is runner state) — they
  ride `MIGroupRecord`.
- **§4.6 No child-side "orphan adoption".** A recovered child whose
  parent record is gone fails loud (ADR-033 v.4 §2.10). The
  alternative — let it run to completion and discard the outputs —
  hides a broken process half-silently; the parent's absence means
  the caller's state is already lost, and loud beats plausible.

## §5 API deltas

| Surface | Change | Compat |
|---|---|---|
| `checkpoint.Document` | + `Calls`, `MIGroups`, `Sweeps`; `TrackRecord.MI`; schema 3→4 | additive; older documents restore |
| `internal/instance` capture/restore | guards retired; position capture; seeded restores | internal |
| `pkg/thresher` invoker | children get checkpointing options | behavioral (children now persist) |
| `instance.Restore` / recovery | call re-link, loud missing-counterpart failures | internal |
| Public API | — none — | |

## §6 Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | schema-4 round-trip (`internal/instance/checkpoint`) | FR-1: new records marshal/unmarshal; schema stamps 4; future refusal intact |
| T-2 | schema-3 fixture restore (`internal/instance`) | FR-9: a pre-fidelity document restores as today; absent records rebuild nothing |
| T-3 | composite double-execution regression (`internal/instance`) | FR-5: restore mid-composite → drained child scope resumes host exactly once; body not re-run |
| T-4 | sequential MI / Standard Loop position (`internal/instance`) | FR-2/FR-3: capture mid-pass-k; restore resumes at pass k with staging intact; fired condition honored |
| T-5 | parallel MI open set (`internal/instance`) | FR-2/FR-4: capture with j of n open; restore re-opens exactly the open ordinals; completed outputs preserved |
| T-6 | sweep capture/restore + window (`internal/instance`) | FR-6: mid-sweep restore resumes the queue in order (Running re-runs); no capture instant loses consumed entries |
| T-7 | durable child + re-link (`pkg/thresher`) | FR-7: kill mid-call → both records exist; recovery re-links; child completes → parent resumes; no duplicate child; `InstancesRoots`/`InstancesChildren` separate the registry and the handle exposes the linkage |
| T-8 | missing-counterpart refusals (`pkg/thresher`) | FR-7: child record deleted → parent restore fails loud; parent record deleted → child fails loud; engine starts regardless |
| T-9 | guard retirement (`internal/instance`) | FR-8: capture succeeds with each construct in flight; dehydration still refuses with the new reason |
| T-10 | e2e kill-and-resume, MI+call composite (`pkg/thresher`) | the §3 worked trace end-to-end |

## §7 Milestones

- **M1 — schema 4 + the silent-sibling fixes.** FR-1, FR-5, FR-2/FR-3
  (sequential); T-1/T-2/T-3/T-4.
  `feat(instance): schema-4 position records; composite scopes and sequential iteration restore at position (SRD-082 M1)`.
- **M2 — parallel MI.** FR-4 (incl. its guard's retirement and the
  codec's explicit nil kind — a pre-sized staging carries holes); T-5.
  `feat(instance): parallel multi-instance groups capture and restore their open set (SRD-082 M2)`.
- **M3 — the compensation sweep.** FR-6; T-6.
  `feat(instance): a resolving compensation sweep survives the checkpoint (SRD-082 M3)`.
- **M4 — durable children + the last guard's retirement.** FR-7, FR-8;
  T-7/T-8/T-9.
  `feat(thresher): Call Activity children are durable and re-linked; the capture deferral retires (SRD-082 M4)`.
- **M5 — the proof + docs.** T-10; guides
  (`operating/persistence.md` "Current limits" rewritten,
  `extending/repository.md` untouched), README, CHANGELOG.
  `feat(instance): the composite kill-and-resume e2e; docs (SRD-082 M5)`.

## §8 Cross-doc

- Implements **ADR-033 v.4** §2.1/§2.2/§2.10 (bumped in this branch)
  and **ADR-023 v.3** §2.7's restart contract (Draft, amended in this
  branch).
- Upstream: **ADR-025 v.2** §2.12, **ADR-026 v.1** §2.1/§2.4/§2.5.
- Related: **SRD-070** (whose FR-4 deferral posture this retires),
  **SRD-071 v.2.5**, **SRD-079** (schema 3 → 4).
- **#277**: closes all three checkboxes, with the recorded outcome
  "restore faithfully" for each (§4.1).

## §9 Definition of Done

- [ ] FR-1…FR-9 implemented; every §6 test exists and passes.
- [ ] `make ci` green; diff-coverage ≥95% (aim 100%); touched
      functions ≥80%.
- [ ] SRD-070's retired deferral is cross-noted (linked-docs sync);
      the persistence guide's "Current limits" rewritten.
- [ ] §10 filled.

## §10 Implementation summary

*Post-landing placeholder.*

## Open questions

*None — §4 records the resolved design points (per-construct outcome,
the position mirror, at-least-once handlers, the re-link seam, derived
scope entries, orphan refusal).*
