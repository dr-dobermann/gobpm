# ADR-033 — Persistence & State: checkpoints, hydration and recovery

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.2 |
| Date | 2026-07-26 |
| Owner | Ruslan Gabitov |
| Refines | [SAD-001 v.1](SAD-001-vision-and-architecture.md) §10 (save/restore as P0: "goroutines are the execution medium, persistence is the state of record"), [ADR-001 v.6](ADR-001-execution-model.md) (the runtime whose state this makes durable), [ADR-007 v.2](ADR-007-in-memory-long-waits.md) (the in-memory long-wait model — dehydration & wake-on-trigger — whose durable half this owns) |
| Related | [ADR-006 v.4](ADR-006-events-and-subscriptions.md) (subscriptions), [ADR-013 v.2](ADR-013-instance-observability.md) (facts vs state), [ADR-014 v.1](ADR-014-message-handling.md) / [ADR-016 v.1](ADR-016-message-correlation.md) (correlation state), [ADR-017 v.1](ADR-017-channel-based-event-processing.md) (the loop as sole state owner), [ADR-021 v.1](ADR-021-service-task-execution-model.md) (the job queue's own durability), [ADR-023 v.3](ADR-023-sub-process-and-call-activity.md) (scopes, child instances), [ADR-025 v.2](ADR-025-activity-iteration-loop-and-multi-instance.md) (iteration state), [ADR-026 v.1](ADR-026-compensation-events.md) (the completion ledger) |

This ADR is the **Persistence & State ADR** that ADR-001, ADR-007 and the
Repository skeleton have been deferring to. It decides what a Process
Instance's **durable state** is, **when** it is written, how an instance
**dehydrates** (long waits release their goroutines), how it
**hydrates** (restart recovery, wake-on-trigger), and what
**suspend/resume** means on top — and what keeps **several engines
sharing one store** safe. Implementation rides the accompanying SRDs,
sliced smallest-first: checkpoint/save/restore, then hydration, then
the operator surface (cluster-safe locking rides the same slices — the
lease is part of the record from the first one).

## 1. Context & problem

- **The standard prescribes the state machines, not the storage.** BPMN
  2.0 defines the observable lifecycles — the activity lifecycle
  (§13.3.2, the vendored extract
  `docs/bpmn-spec/state-machines/activity-lifecycle.md`: "State
  transitions are observable points where engine MUST persist state,
  emit events, and apply data assignments") and the process lifecycle
  (`process-lifecycle.md`). It is silent on persistence mechanics —
  the storage model is an engine concern, but the **transition points
  are normative**: they are where persisted state must be consistent.
- **The engine's own commitments.** SAD-001 §10: save/restore is P0;
  checkpoints align with lifecycle transitions; on restart the runtime
  queries the Repository for in-flight instances and rehydrates —
  "recovery should be straightforward and bounded, not a fragile
  dance". ADR-001's invariant (relocated into ADR-007 §3): a track's
  continuation state is **fully described by position, track/step
  state, Scope data and lineage** — no hidden state on a token object;
  per-node resumable state is reached through a **per-node state
  contract**, never stored on the shared, immutable node definitions.
- **What exists today.** The Repository extension is a deliberate
  skeleton (save/load/delete/list-in-flight over an opaque record)
  whose package doc defers the durable contract here. Long waits park
  a goroutine on a channel (ADR-017's model) — cheap, but nothing
  survives a restart, and a UserTask waiting three days still holds
  its goroutine. Suspend/resume does not exist; the observability
  taxonomy reserves `Paused`/`Resumed` slots for it (ADR-013 v.2).
- **The state to capture has grown.** Since the runtime core landed,
  an instance's in-flight state spans: the scope tree's data
  ([ADR-010 v.2](ADR-010-process-data-model.md) / [ADR-011 v.7](ADR-011-process-data-flow.md)), the track table, event subscriptions and armed
  handlers (ADR-006, ADR-023), conversation keys (ADR-016), iteration
  state (ADR-025), the completion ledger with its data snapshots
  (ADR-026), parked human tasks and enqueued jobs (ADR-020/021).
  Deciding **which of these is state-of-record and which is derivable**
  is the heart of this ADR.

## 2. Decision

### 2.1 The checkpoint document — minimal state, derived arming

An instance's durable state is one **checkpoint document**, written
atomically per instance, containing only what cannot be re-derived:

1. **Identity & pin**: instance id, the process reference **version-
   pinned** (latest-at-launch resolution happened at start — recovery
   re-clones the SAME registered version; a missing version fails
   recovery loud).
2. **Status**: the persisted lifecycle status — `Active`, `Suspended`,
   or a terminal `Completed`/`Terminated` (the Repository's status set
   gains `Suspended`).
3. **The scope tree's data**: every open scope's committed data (path,
   datum name, value, state), parent links — the ADR-011 data plane at
   its last consistent commit. Values serialize through the engine's
   canonical value model (single values, arrays, records, maps —
   ADR-011 v.7); an unserializable payload is a **loud checkpoint
   error**, not a silent skip.
4. **The track table**: per live track — position (node id), the
   track/step state, lineage (fork parentage), and its scope path.
   Tokens are projections (ADR-001) — persisting the track table IS
   persisting the tokens.
5. **Wait descriptors** — the per-node state contract of ADR-007 §3,
   realized as serializable descriptors: a timer's absolute deadline
   and cycle position; a parked human task's id and
   announced/taken state; a message/signal wait's subscription key
   and correlation keys (ADR-016's conversation key-set); an event
   gateway's armed alternatives; partially-complete iteration state
   (ADR-025's counters and collected outputs).
6. **The completion ledger** (ADR-026): entries with their ordinals
   and data snapshots — compensability must survive a restart.

Everything else is **derived at hydration by re-walking the re-cloned
graph**: armed boundaries and event-sub-processes, routing tables,
subscriptions re-registered from the wait descriptors, condition
re-evaluation wiring. Node definitions are shared and immutable — they
are never serialized; the registered process (at the pinned version)
is the schema, the checkpoint is the data. The document carries a
**schema version** for forward migration.

### 2.2 Checkpoint policy — the loop writes, transitions gate

- **The loop is the single writer.** All instance state mutates on the
  instance's event loop (ADR-001/ADR-017), so a checkpoint taken by
  the loop between event applications is a **consistent cut** by
  construction — no locks, no torn state.
- **Checkpoints align with observable lifecycle transitions** (the
  normative persist points of §13.3.2): instance created / completed /
  terminated / failed; a node reaching `Completed` (its data outputs
  committed); a track parking on a long wait; a scope opening/closing;
  a data commit outside those (a standalone `DataChange`) rides the
  next transition. Mid-step state (a half-executed service call) is
  **never** checkpointed — a step is atomic between transitions, and
  recovery re-enters the node, not the half-step.
- **The write mode is a policy seam** with a safe default: synchronous
  write-through on terminal and wait transitions (the states that must
  never be lost), write-behind batching allowed for intermediate
  progress transitions. The embedder tunes durability vs throughput
  without touching the engine; the zero-config in-memory default makes
  every checkpoint a cheap in-process store.

### 2.3 Effects are at-least-once; state is the record

A checkpoint and the effects around it (outbound messages, worker
jobs, observer facts) cannot be atomic across seams, so the model is
explicit:

- **State (checkpoints) is exactly-once by construction** — one
  writer, one record, replace-on-write with a version check (a stale
  overwrite is a loud error, the split-brain guard).
- **Effects are at-least-once.** Recovery may re-emit an outbound
  message or re-announce a task whose pre-crash send raced the
  checkpoint. The receiving seams already tolerate this: message
  correlation dedups by key (ADR-016), the job queue reconciles by
  lock state (ADR-021 owns its own durability and reclaim), the task
  distributor re-announces parked tasks idempotently (the
  announce/take model of [ADR-020 v.1](ADR-020-human-interaction-execution-model.md)).
- **Facts are observability, not state** (ADR-013): the observer
  stream is never replayed from storage and never read back to rebuild
  state; recovery emits fresh facts about what recovery did.

### 2.4 Dehydration — the durable half of the long-wait model

ADR-007's in-memory seed (subscription registered → goroutine ends →
fresh track on trigger) becomes the **memory projection of the same
model** this ADR makes durable:

- A track reaching a long wait writes its wait descriptor into the
  checkpoint (§2.2's wait transition), registers the in-memory
  subscription, and **its goroutine ends**. The instance keeps
  bookkeeping only; with every track dehydrated the whole instance may
  release its loop until a trigger arrives (idle instances cost
  memory, not goroutines).
- The trigger path is unchanged for a resident instance (ADR-006
  delivery); for a released instance the trigger **hydrates first**
  (§2.5), then delivers.
- ADR-007 is finalized (Draft → Accepted) by the hydration SRD as the
  in-memory statement of this section — one model, two residency
  levels.

### 2.5 Hydration & recovery

- **Wake-on-trigger**: an arriving trigger for a non-resident instance
  loads the checkpoint, re-clones the pinned process version, replays
  the derivation walk (arming, routing), respawns tracks at their
  recorded positions, then delivers the trigger through the normal
  path. Hydration is the same code path recovery uses for one
  instance.
- **Restart recovery**: the runtime lists in-flight instances and
  hydrates each — eagerly for instances with due work, lazily
  (on-trigger) otherwise; the policy is an engine option, the default
  eager-and-bounded.
- **Recovery semantics per wait kind**: an **overdue timer** fires
  once, immediately (missed cycle repetitions collapse into one firing
  — recovering yesterday's every-5-minutes timer must not fire 288
  times); a **parked human task** is re-announced through the
  distributor (a `Taken` task keeps its taken state; completion after
  recovery follows the normal path); a **message/signal wait**
  re-registers its subscription — messages that arrived while down
  were never accepted, senders retry (at-least-once, §2.3); an
  **in-flight job** is the dispatcher's concern — its queue survives
  by its own contract (ADR-021), and the engine's recovered node waits
  for the job outcome exactly as it did before the restart.
- Recovery failures are loud and per-instance: one corrupt checkpoint
  or missing process version marks THAT instance failed-to-recover
  (observable, operator-visible) and never blocks the rest.

### 2.6 Suspend & resume — the operator surface on top

- **Suspend (per instance)**: stop dispatching new steps; in-flight
  steps run to their next transition (quiesce, bounded by the step
  granularity of §2.2); checkpoint; mark `Suspended`; release the
  runtime state (a suspended instance is a dehydrated instance with a
  status that refuses triggers). Triggers arriving while suspended are
  refused to the sender's retry (messages) or held by their seams
  (timers re-arm on resume relative to their absolute deadlines; human
  tasks stay taken/announced but completion is refused until resume).
- **Resume**: hydrate (§2.5) + status back to `Active` + deliver
  whatever is due (overdue timers fire once, §2.5).
- **Engine-level pause** (the whole runtime) is the same quiesce
  applied to all resident instances; it fills the reserved
  `Paused`/`Resumed` observability slots (ADR-013 v.2). Suspension is
  state (it survives a restart); pause is runtime posture (it does
  not).

### 2.7 The Repository is one port among peers — the storage composition rule

The Repository is **the engine's instance-checkpoint port, nothing
more** — one narrow port among peers, not the system's persistence
facade. The rule generalizes across every storage-backed module:

- **One port per consumer.** Each subsystem defines its own
  domain-shaped storage port: the engine's checkpoint store (this
  Repository), the dispatcher's job store when it goes durable
  (ADR-021 owns its queue), the DataStore port, an AuthN/Z plugin's
  own store, a future history sink. No port serves two masters, so no
  port ever needs generic query/DDL surface — a "universal Repository"
  would degenerate into either an unqueryable blob store or a
  homemade data-definition language (§4).
- **The shared thing is the backend handle, and it is the user's.**
  Storage-backed modules accept their backend handle at construction
  (a `database/sql` handle, a driver pool, a document-store client) —
  the stdlib and driver ecosystems ARE the "db connector", the engine
  never wraps or owns one. One handle, created by the embedder, feeds
  the engine's checkpoint store and any plugin's store alike — the
  existing composition rule promoted from adapters to all storage
  users ([ADR-003 v.1](ADR-003-module-layout.md) §4.4: no cross-module
  imports; the user composes shared resources at construction time).
- **Namespaced schemas, per-module migrations.** Every storage-backed
  module owns a namespaced slice of the shared database (a schema or
  table prefix) with its own embedded migrations — DDL and dialect
  live inside the module's storage implementation, where they are
  legitimate; the engine core stays storage-blind (the SAD-001 v.1 §3
  G2 dependency posture — the zero-config engine keeps its in-memory
  defaults with no database anywhere).
- **The `Migrator` capability convention** (optional, one method:
  prepare your own objects) lets bootstrap walk the wired extensions
  and have each create its schema — the disciplined form of "every
  module manages its own db objects"; never a core DDL framework.

The skeleton itself grows only what this model needs: the `Suspended`
status; a **record version for compare-and-set** saves (the §2.3
split-brain guard) plus the §2.8 ownership lease; the checkpoint
document as an opaque, schema-versioned byte payload (the
serialization model is the engine's, the storage's job is bytes);
listing filtered by status and ownership. History stores, inboxes and
pagination stay out — a future history ADR's territory.

### 2.8 Many engines, one store — cluster-safe locking

Several engine processes MAY share one database. This layer owns the
**correctness** of that sharing; the *distribution* of work stays with
ADR-008 (§5). The model:

- **Instance ownership is a lease.** An engine claims an instance
  before running it (at start, hydration, or wake-on-trigger): a
  per-instance lease record — owner engine id + incarnation, expiry —
  written with the same CAS discipline as the checkpoint.
  Lease renewal rides the instance's checkpoint writes plus a
  low-frequency heartbeat for long-quiet instances; completion,
  dehydration and suspension release the lease.
- **CAS is the fencing token.** Every checkpoint save carries the
  record version AND the owner's lease incarnation; a save from an
  engine whose lease expired or was reclaimed **fails loud** — a
  zombie engine (paused VM, network partition survivor) can never
  overwrite the new owner's state. The failed engine drops the
  instance and re-reads (its local state is disposable — the store is
  the record, §2.3).
- **Claim-first wake semantics.** Recovery listing returns unowned or
  lease-expired instances only; wake-on-trigger claims before
  hydrating — the first claimer wins, losers drop the attempt and the
  trigger's at-least-once seam (message retry, timer re-check, task
  re-announce) routes it to whoever owns the instance. Overdue-timer
  scans in a fleet are therefore naturally deduplicated: firing
  requires the claim.
- **Orphan recovery is lease expiry.** A crashed engine's instances
  become claimable when their leases lapse — no coordinator, no
  membership protocol; the store's CAS is the only synchronization
  primitive this layer needs. Lease reclaim is observable (an
  operator-visible fact naming both engines).
- **Deployment parity is the operator's contract.** A checkpoint pins
  its process version (§2.1); an engine can only claim instances whose
  pinned version it has registered — a claim against an unregistered
  version is refused loud. Distributing definitions themselves is
  ADR-008 territory.

### 2.9 Observability

Recovery and residency are operator-relevant, low-volume milestones:
hydration/dehydration and suspend/resume/recovery emit instance-scoped
facts at lifecycle volume (never per-checkpoint — a checkpoint
accompanies an already-observable transition; a per-checkpoint fact
would double the stream for zero information). The exact
kinds/phases/details ride the SRDs under ADR-013 v.2's open taxonomy
and masking rules (names and counts, never payload values).

## 3. Grounding

| Claim | Source |
|---|---|
| Transitions are the normative persist points ("engine MUST persist state" at transitions) | the vendored extract, `state-machines/activity-lifecycle.md` (BPMN §13.3.2, Fig. 13.2) |
| The process-level lifecycle recovery re-enters | `state-machines/process-lifecycle.md` |
| Save/restore is P0; checkpoint-at-transition; bounded recovery | SAD-001 v.1 §10 |
| Track state = position + state + scope data + lineage; per-node state contract; immutable shared definitions | ADR-007 v.2 §3 (relocated from ADR-001) |
| The loop as sole owner of instance state (the consistent-cut premise) | ADR-001 v.6, ADR-017 v.1 |
| Correlation keys / dedup-by-key on redelivery | ADR-014 v.1, ADR-016 v.1 |
| The job queue's own durability, lock reclaim | ADR-021 v.1 §2.4/§2.7 |
| The completion ledger must survive (compensability of completed work) | ADR-026 v.1 §2.7 |
| The BPMN standard is silent on storage mechanics | no clause governs persistence in §13–§14; the extract covers only the state machines |
| Instance-level distribution rides sticky ownership + persistence rehydration | SAD-001 v.1 §13 (preliminary; ADR-008's future home) |
| Composition at construction; no cross-module imports | ADR-003 v.1 §4.4 (the depguard-enforced rule §2.7 promotes) |

## 4. Alternatives considered

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A. **Event sourcing** — append-only fact/command log, state rebuilt by replay | perfect audit; natural history | replay demands deterministic re-execution, which service tasks, scripts and external calls break; unbounded recovery time; the observer stream would become load-bearing state, inverting ADR-013 | ❌ rejected — checkpoints are bounded and "not a fragile dance" (SAD §10); the fact stream stays observability |
| B. **Persist everything** incl. derived arming/routing | hydration = pure load, no derivation walk | duplicates what the immutable graph already encodes; every arming change becomes a schema migration; bigger documents, more drift surface | ❌ rejected — minimal state + derivation keeps the checkpoint stable across engine evolution |
| C. **Goroutine release first, durability later** (pure ADR-007 now) | quick memory win for huge waiting populations | rewires the same wait bookkeeping twice; invents an ephemeral wait-state shape the durable model would redefine; resume path never exercises recovery | ❌ rejected — this ordering discussion is what triggered this ADR; the wait descriptor is designed once, durably |
| D. **A universal Repository** serving every module's persistence (engine state, authz, jobs, history through one interface) | one seam to learn | irreconcilable data shapes force generic query/DDL surface — a homemade data-definition language and de-facto ORM; every consumer's schema churn becomes interface churn | ❌ rejected — one narrow port per consumer (§2.7) |
| E. **A shared db-driver seam in core** (modules speak SQL through an engine-owned connector) | full query power per module; one connection story | picks a storage paradigm for everyone (non-SQL backends become second-class); the zero-config in-memory engine becomes the special case; core inherits a dialect against the SAD G2 posture; and the ecosystem already ships the connector (`database/sql` + drivers) | ❌ rejected — the handle is the user's, shared at construction (§2.7) |
| F. **Checkpoint snapshot + derived arming + per-node wait descriptors** (chosen) | one writer, consistent cuts for free; bounded recovery; ADR-007 becomes a projection | requires the serialization discipline of §2.1 (canonical values, loud on unserializable) | ✅ chosen |

## 5. Deferrals

- **Work distribution** (sticky routing, rebalancing, cluster-wide
  signal broadcast and correlation indexes, definition distribution) —
  ADR-008 (SAD §13), on top of this layer's state of record and §2.8
  locking. The boundary: THIS ADR makes engines sharing a store
  **safe**; ADR-008 makes them **cooperative**.
- **History / audit store** (queryable execution history beyond the
  live checkpoint) — a future ADR; the observer stream remains the
  live audit today.
- **Cross-version instance migration** (#95) — the checkpoint pins its
  process version; migrating a pinned instance is its own workstream.
- **Incident store** (#80) — failed-to-recover instances surface as
  observable failures now; a durable incident queue comes with the
  fault-tolerance epic.

**The accompanying SRDs** (smallest-first): the checkpoint document +
save/restore + restart recovery over the Repository seam; then
dehydration/hydration (goroutine release, wake-on-trigger, ADR-007
finalized); then suspend/resume and the engine pause.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.2 | 2026-07-27 | Ruslan Gabitov | Pin refresh: ADR-007 authored in full and Accepted (v.2 — the in-memory dehydration & wake-on-trigger mechanism this ADR's §2.4/§2.5 delegated to), so the §Refines and §3-grounding pins move v.1 → v.2. No content change to the durable model. |
| v.1 (Accepted) | 2026-07-27 | Ruslan Gabitov | Accepted with the first landing slice (the accompanying checkpoint/recovery SRD): the checkpoint document, the grown Repository (CAS + lease), consistent-cut capture, restart recovery with re-enter semantics and the recorded-deadline timers — all proven live, incl. the §2.8 fencing (a zombie engine's saves rejected) and the ADR-005-style incremental plan: dehydration/wake-on-trigger and suspend/resume ride the remaining slices. One §2.8 sharpening surfaced by the landing: deployment parity covers ELEMENT IDENTITY too — recovery requires stable node ids across engines (pinned ids or a serialized model). |
| v.1 | 2026-07-26 | Ruslan Gabitov | Initial draft — the deferred Persistence & State conception: one checkpoint document per instance (identity + pinned version, status incl. `Suspended`, scope-tree data, the track table, per-node wait descriptors, the completion ledger; armed/routing state derived at hydration, never stored; schema-versioned, loud on unserializable values); the loop's consistent-cut checkpoint at the normative lifecycle transitions with a pluggable write mode; exactly-once state / at-least-once effects (correlation dedup, job-queue reclaim, idempotent re-announce); dehydration as the durable half of ADR-007's model (one model, two residency levels); wake-on-trigger hydration = single-instance recovery, per-wait-kind semantics (overdue timers fire once, tasks re-announce, subscriptions re-register); suspend/resume as status over the same machinery, engine pause filling the reserved observability slots; the Repository grows CAS + `Suspended` + opaque schema-versioned payloads. Event sourcing, persist-everything and hydration-before-durability rejected. The storage composition rule: the Repository is the checkpoint port only — one narrow port per storage consumer, the backend handle user-owned and shared at construction (no universal Repository, no db-driver seam in core — both named rejected alternatives), namespaced schemas with per-module migrations and the optional `Migrator` capability. Cluster-safe sharing (§2.8): per-instance ownership leases with CAS fencing, claim-first wake, lease-expiry orphan recovery, loud deployment-parity refusals — safety here, work distribution deferred to ADR-008. Implementation rides the accompanying SRDs. |
