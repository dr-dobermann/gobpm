# SRD-070 — The instance checkpoint, save/restore and restart recovery

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-26 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-033 v.1](../design/ADR-033-persistence-and-state.md) §2.1–§2.3, §2.5 (restart recovery), §2.7 (the Repository growth), §2.8 (the lease — carried in the record from this first slice), §2.9 |
| Upstream | [ADR-001 v.6](../design/ADR-001-execution-model.md), [ADR-013 v.2](../design/ADR-013-instance-observability.md), [ADR-017 v.1](../design/ADR-017-channel-based-event-processing.md) |

The first ADR-033 slice: the **canonical value codec**, the **checkpoint
document**, loop-side **capture at lifecycle transitions**, the
**Repository contract growth** (CAS + lease + opaque payload), and
**restart recovery** (re-enter-the-node semantics, overdue timers fire
once). Dehydration/wake-on-trigger (SRD-071) and suspend/resume
(SRD-072) build on this record.

## §1 Background (the state inventory, verified)

- **The runtime state splits three ways**: the immutable per-instance
  template (`snapshot.Snapshot` — nodes/flows/properties/data-objects/
  correlation keys, `internal/instance/snapshot/snapshot.go:18`), the
  **loop-confined maps** (`loopState`, `internal/instance/loop.go:20` —
  `position`, `waiting`, `msgIdx`, `parked`, `tasks`, `jobs`, `calls`,
  `scopes`, `miGroups`, `ledgers`, `sweeps`, `conds`), and the
  per-track fields (`track`, `internal/instance/track.go:160` —
  `steps` (position = last element, `track.go:709`), `prev` lineage,
  `scopePath`/`scopeSeg`, `msgDefIDs`, `taskID`, `loopCounter`,
  `state`).
- **The data plane is one map**: `scope.Scope.scopes` — `DataPath →
  (name → data.Data)` (`internal/scope/scope.go:28`); the scope tree
  is implicit in the path strings. Conversation keys live in
  `correlator.keys` (`internal/instance/correlation.go:21`); the
  compensation ledger in `loopState.ledgers` with per-entry data
  snapshots (ADR-026).
- **No value serialization exists**: `data.Value` offers
  `Get/Update/Clone` only (`pkg/model/data/value.go:8`); the concrete
  kinds are `values.Variable[T]`, `Array[T]`, `Record`, `Map[T]`; the
  only JSON in the model layer is `errs.ApplicationError`.
- **The version is not stamped on the instance**: it exists only on
  `ProcessRegistration` (`pkg/thresher/registration.go:16`) — an
  instance/snapshot cannot name the version it runs
  (`snapshot.Snapshot` carries no version field).
- **The Repository is a dormant slot**: the engine never calls
  `Save/Load/ListInFlight`; `InstanceRecord.State`/`Status` are never
  populated (`pkg/repository/repository.go:34`, wired at
  `pkg/thresher/options.go:122` with no non-test consumer).
- **Timer deadlines are runtime-only**: the absolute `next` lives in
  the live `timeWaiter` goroutine
  (`internal/eventproc/eventhub/waiters/timer.go:29`) — nothing on the
  track records when a parked timer fires; re-entering a Duration
  timer would restart the duration (drift), which ADR-033 §2.1.5
  forbids.
- `InstanceHandle.Suspend/Resume` are reserved stubs
  (`pkg/thresher/handle.go:127`); the instance run-state enum is
  `Created/Active/Completed/Terminating/Terminated`
  (`internal/instance/lifecycle.go:20`).

## §2 Requirements

### §2.1 Functional

- **FR-1 — the version pin.** `snapshot.Snapshot` gains `Version int`,
  stamped by `RegisterProcess` when the registration mints it; child
  launches (`invoker`) and `NewFromEvent` carry it. The checkpoint
  names `(ProcessID, Version)`; recovery resolves exactly that
  registered version or fails THAT instance loud (`ObjectNotFound`,
  naming both).
- **FR-2 — the canonical value codec** (new package
  `internal/instance/checkpoint`, unexported codec files). Encodes a
  `data.Data` set into tagged JSON and back: scalars by kind tag
  (`bool`, `string`, `float64` + the integer family as recorded Go
  kind, `time.Time` as RFC3339Nano), `Array`/`Record`/`Map` by
  structural traversal (the `Collection`/`Record`/`Map` capabilities),
  nested composition included. Decode rebuilds `values.*` kinds and
  re-wraps `Parameter`/`ItemAwareElement` with their recorded
  name/state. **An unencodable payload** (a native-struct `Variable[T]`
  without structural capabilities, a functor, a channel) **is a loud
  classified error naming the scope path and datum** — never a silent
  skip (ADR-033 §2.1.3).
- **FR-3 — the checkpoint document** (schema-versioned, `Schema: 1`):
  identity (instance id, parent instance id + call node id when a
  child), the FR-1 pin, status, **the scope table** (every open
  `DataPath` → encoded data), **conversation keys**, **the ledger**
  (per scope path: ordinals + encoded snapshots + handler refs), and
  **the track table** — live tracks only (states `Created/Ready/
  WaitForEvent/AwaitSync`): id, lineage (`prev`), scope path + segment,
  state, current node id, `loopCounter`, `taskID`, `msgDefIDs`, and
  the **wait descriptor** where one exists (this slice: the timer's
  absolute deadline + remaining cycles, captured at arming — the
  waiter's computed `next` recorded through a new park-time hook).
  Ended/merged tracks are NOT persisted — their contribution lives in
  the scope data; join reachability recomputes from live positions.
  The record envelope carries the **lease** (owner engine id +
  incarnation + expiry) and the **record version** for CAS (ADR-033
  §2.8 — present from this slice; single-engine semantics exercise it,
  multi-engine contention tests ride SRD-071+).
- **FR-4 — capture on the loop.** A `checkpoint()` step on the
  instance loop builds the document from `loopState` + tracks + scope
  (goroutine-confined = consistent cut, no locks) after applying:
  instance activation, `evEnded` (a node completed), the wait parks
  (`evWaiting`/`evTaskWaiting`/`evJobWaiting`), scope open/close, and
  the terminal transitions. **Unsupported in-flight constructs defer
  the checkpoint loudly, not fatally**: non-empty `calls`, `miGroups`
  or `sweeps` skip the write and emit the degradation fact (FR-8) —
  the instance runs on volatile; the next supported transition
  retries. (Full-fidelity capture of those constructs is SRD-071+
  territory; refusing silently or killing the instance are both
  wrong.)
- **FR-5 — the Repository growth** (`pkg/repository`):
  `InstanceRecord` becomes `{ID, Status, Payload []byte, RecVersion
  int64, Lease{Owner string, Incarnation int64, Expiry time.Time}}`;
  `Status` gains `StatusSuspended` (reserved for SRD-072); `Save`
  becomes CAS (`RecVersion` must match the stored one; mismatch is a
  classified error under a new `errs.ConcurrentUpdate` class constant
  (the CAS-conflict vocabulary the fencing checks match on)); `ListInFlight` returns
  non-terminal records with expired-or-absent leases only. `memrepo`
  implements the full contract (value copies, no more
  store-by-reference).
- **FR-6 — restore.** `instance.Restore(doc, snapshot, deps)` rebuilds
  an instance without running start seeding: reopen the recorded scope
  paths, decode + recommit the data, rebuild correlator keys and
  ledgers, recreate the live tracks at their recorded nodes
  (**re-enter semantics** — each track respawns entering its current
  node fresh: subscriptions re-register from the node's definitions,
  a user task re-announces under a fresh task id, a job re-enqueues —
  the ADR-033 §2.3 at-least-once effects; a **timer wait re-arms at
  the RECORDED deadline**, not a re-evaluated one — overdue fires
  once, immediately; remaining cycles continue from the recorded
  count).
- **FR-7 — restart recovery.** `thresher.Run` (after the hub starts):
  `ListInFlight` → per instance: claim the lease (CAS), `Load`, decode,
  resolve the pinned registered version (FR-1), `Restore`, run. Every
  failure is **per-instance and loud** (an operator-visible fact + the
  instance left unclaimed/failed) and never blocks the rest. An engine
  with no repository configured (the zero-config default has memrepo —
  in-process, empty at boot) recovers nothing and starts clean.
- **FR-8 — observability** (ADR-013 v.2 open phases; lifecycle
  volume): `KindInstanceState` gains **`Recovered`** (Info — recovery
  completed for an instance, details: process id/version, live-track
  count) and **`CheckpointDeferred`** (Warn override — the FR-4
  degradation, details naming the deferring construct). No
  per-checkpoint fact (ADR-033 §2.9).

### §2.2 Non-functional

- **NFR-1** — checkpoint capture runs on the loop: the write itself is
  handed off (the ADR-033 §2.2 write mode; this slice ships the
  synchronous default only, the policy seam arrives when a second mode
  exists).
- **NFR-2** — validate-all-params; no `Must*` in library paths; loud
  classified errors per the errs idiom.
- **NFR-3** — masking: checkpoint facts carry names/counts, never
  payload values.
- **NFR-4** — coverage: `make ci` green; diff-coverage ≥95% (aim
  100%); touched functions ≥80%.

## §3 Models (shapes)

```go
// internal/instance/checkpoint — the document (Schema 1)
type Document struct {
	Schema      int
	InstanceID  string
	ParentID    string // + CallNodeID — child linkage, informational
	CallNodeID  string
	ProcessID   string
	Version     int // the FR-1 pin
	Status      string
	Scopes      []ScopeRecord   // path + encoded data
	ConvKeys    map[string]string
	Ledgers     []LedgerRecord  // path, ordinal, handler ref, snapshot
	Tracks      []TrackRecord   // live tracks only
}

type TrackRecord struct {
	ID, State, NodeID string
	Prev              []string
	ScopePath, ScopeSeg string
	LoopCounter       int
	TaskID            string
	MsgDefIDs         []string
	Timer             *TimerDescriptor // deadline + cycles left, nil unless timer-parked
}

// pkg/repository — the grown contract (FR-5)
type Lease struct{ Owner string; Incarnation int64; Expiry time.Time }
type InstanceRecord struct {
	ID         string
	Status     Status // + StatusSuspended
	Payload    []byte // the schema-versioned document, opaque
	RecVersion int64  // CAS
	Lease      Lease
}
```

Worked trace (T-6's shape): a process parks on a 2h Duration timer →
the park transition checkpoints (deadline recorded absolute); the
engine stops; a new engine over the same repository starts →
`ListInFlight` → claim → restore → the track re-enters the timer node
and re-arms **at the recorded deadline**; `InstanceState/Recovered`
observed; the clock advances past the deadline → the timer fires once
→ the instance completes. The same trace with the deadline already
past at recovery: the timer fires immediately, once.

## §4 Analysis & decisions

- **§4.1 Live-tracks-only persistence.** Ended/merged tracks exist for
  the history projection; their execution effect is already in the
  committed scope data. Persisting them would double the document for
  a projection that recovery legitimately resets (history restarts at
  recovery — documented).
- **§4.2 Re-enter-the-node, plus one recorded exception.** Re-entering
  reuses the nodes' own arming code (no parallel restore-arming
  logic) and realizes at-least-once effects. The single exception is
  the timer deadline — re-evaluation would restart Durations, so the
  descriptor overrides the waiter's computed `next` (ADR-033 §2.5's
  overdue-fires-once needs the original deadline).
- **§4.3 Defer-don't-die on unsupported constructs.** Killing an
  instance because its checkpoint cannot capture an in-flight MI group
  would make persistence a regression; silently skipping would fake
  durability. The Warn fact + retry-next-transition keeps execution
  intact and the degradation visible until SRD-071+ closes the
  fidelity gap.
- **§4.4 The codec lives with the checkpoint, not on the values.**
  Adding `MarshalJSON` to `values.*` would freeze a wire format into
  the public value API; the checkpoint codec owns the tagged format
  privately and can version it with the document schema.
- **§4.5 The lease rides from day one** (ADR-033 §2.8): the record
  shape with CAS + lease is the contract every adapter implements once
  — retrofitting fencing later would be a breaking storage migration.

- **§4.6 The hub is derived state.** No hub-side registration is ever
  checkpointed; recovery rebuilds the hub by re-entering the wait
  nodes (fresh hub, same arming code). Per kind: **message** —
  re-registers under the same catch-definition ids with the restored
  conversation keys; crash-window deliveries return via sender retry +
  correlation dedup (§2.3). **Timer** — re-arms at the recorded
  deadline (the §4.2 exception); nothing depends on lost hub state.
  **Conditional** — self-healing: re-arming re-runs the initial
  evaluation over restored data, so a condition that turned true
  during downtime fires immediately on recovery. **Signal** —
  ephemeral by the standard's broadcast semantics: a signal in the
  crash window or during downtime is lost exactly as for a
  late-arming catcher in normal operation (a durable inbox is
  deliberately out of scope). **Instantiating starters** — process-
  level, rebuilt at engine boot when definitions re-register, which
  is why recovery runs after the hub starts (FR-7). **In-flight hub
  buffers** — volatile, covered by the same at-least-once posture.

## §6 Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | codec unit (`internal/instance/checkpoint`) | FR-2: round-trip for every canonical kind incl. nesting (record-in-map, array-of-records), integer-family fidelity, time precision; loud on an uncodable payload naming path+datum |
| T-2 | document capture (`internal/instance`) | FR-3/FR-4: a parked instance's document carries scopes/keys/ledger/live tracks + the timer descriptor; ended tracks absent; deferral on in-flight calls/MI/sweeps emits `CheckpointDeferred` and skips the write |
| T-3 | repository contract (`pkg/repository/memrepo`) | FR-5: CAS accept/reject, lease fields round-trip, `ListInFlight` honors terminal status and live leases, value-copy semantics |
| T-4 | restore unit (`internal/instance`) | FR-6: scopes/data/keys/ledger rebuilt equal; live tracks respawn at their nodes; version mismatch fails loud |
| T-5 | version pin (`pkg/thresher`) | FR-1: the snapshot carries the registration's version; recovery against an unregistered version fails that instance loud, others recover |
| T-6 | restart recovery e2e (`pkg/thresher`) | FR-7/FR-8: park on timer → stop → new engine, same repo → `Recovered` fact → the timer fires at the recorded deadline (and the overdue variant fires once immediately); a user-task park recovers by re-announce; a message park re-subscribes and completes on send; a conditional wait whose condition turned true during downtime fires on recovery (§4.6) |
| T-7 | lease/CAS (`pkg/thresher` + memrepo) | FR-5/FR-7: recovery claims; a second engine's stale save is rejected (fenced) |

## §7 Milestones

- **M1 — the codec.** FR-2; T-1.
  `feat(checkpoint): the canonical value codec (SRD-070 M1)`.
- **M2 — the document, the pin, the repository growth.** FR-1/FR-3/
  FR-5; T-3 + the capture halves of T-2.
  `feat(checkpoint): the checkpoint document and the grown Repository (SRD-070 M2)`.
- **M3 — loop capture + facts.** FR-4/FR-8; T-2 complete.
  `feat(instance): consistent-cut checkpoints on the loop (SRD-070 M3)`.
- **M4 — restore + restart recovery + e2e.** FR-6/FR-7; T-4…T-7;
  CHANGELOG + README notes; the restart-recovery example
  (`examples/restart-recovery` — two engines over one shared memrepo
  in one process demonstrate the crash/recover trace).
  `feat(thresher): restart recovery over the Repository (SRD-070 M4)`.

## §8 Cross-doc

- Implements **ADR-033 v.1** §2.1–§2.3/§2.5/§2.7–§2.9 (first slice).
- Upstream: **ADR-001 v.6**, **ADR-013 v.2**, **ADR-017 v.1**.
- SRD-071 (dehydration/wake-on-trigger) and SRD-072 (suspend/resume)
  build on this record; **#84** (timer durability) closes with the
  timer-descriptor recovery landing here + SRD-071.

## §9 Definition of Done

- [ ] FR-1…FR-8 implemented; every §6 test exists and passes.
- [ ] `make ci` green; diff-coverage ≥95% (aim 100%); touched
      functions ≥80%.
- [ ] The example runs (crash/recover trace, exit 0); READMEs/examples
      index/CHANGELOG synced.
- [ ] §10 filled.

## §10 Implementation summary

*Filled at landing.*

## Open questions

*None — §4 resolves the design points inline.*

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-26 | Ruslan Gabitov | Initial draft — ADR-033's first slice: the canonical value codec (tagged JSON over the structural capabilities; loud on uncodable payloads), the Schema-1 checkpoint document (scopes, conversation keys, ledger, live tracks with the timer deadline descriptor; ended tracks excluded), the version pin onto the snapshot (closing the restore-attribution gap), the grown Repository contract (opaque payload, CAS record version, the §2.8 lease from day one, `StatusSuspended` reserved), loop-side consistent-cut capture with defer-don't-die on in-flight calls/MI/sweeps (`CheckpointDeferred` at Warn), restore with re-enter-the-node semantics (one exception: timers re-arm at the recorded deadline, overdue fires once), restart recovery in `thresher.Run` (per-instance loud failures, `Recovered` at Info), and the two-engines-one-repo example. |
