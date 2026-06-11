# ADR-010 — Process Data Model

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-11 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-001 v.5 Execution Model](ADR-001-execution-model.md) |

> **Seed — not yet authored in full.** This ADR reserves the slot and records the
> direction for the **process data model** — how per-execution / per-track data
> flows during node execution. It is authored in full when the data-model
> workstream lands (with its own SRD and code in the same branch). The Parallel
> gateway (ADR-005) has higher priority and comes first; nothing here is
> implemented yet.

## 1. Context

A node currently stores **per-execution** data as **mutable fields on the node
object** — the scope route-key (`Event.dataPath`, `activity.dataPath`), and exec
carriers (`ExclusiveGateway.scope`, `UserTask.resChan`, the `ServiceTask`
operation's message items). The Scope is keyed by node **name** (`DataPath =
/<nodeName>`), with no track/execution identity. After [ADR-009 v.1](ADR-009-per-instance-node-graph.md)
there is one node object per instance, so **two tracks of the same instance
executing the same node would clobber each other's per-execution data** (and the
second `ExtendScope` even errors on the duplicate name). [ADR-001 v.5 §6](ADR-001-execution-model.md)
flags this: `NodeDataLoader.RegisterData` mutating the shared node violates the
§4.7 immutability invariant and races on a non-synchronizing merge.

This is distinct from ADR-009: ADR-009 owns node **lifetime** state (join
arrivals, timer position, subscriptions — shared per-instance). This ADR owns
**per-execution** data (the inputs/outputs/scope of *one* node execution by *one*
track). The clobber is **latent today** (no Parallel gateway / join yet to make
two tracks cross one node) but real by construction — and exactly what Parallel
will expose.

## 2. Direction (seed — to be expanded)

- **Persistent data lives in BPMN *container* scopes — process root, subprocess —
  not on tracks and not on nodes.** These are the live, shared, current stores
  (the existing instance scope tree, guarded by the instance `RWMutex`). There is
  **no "track scope"**: a track is the execution thread, not a scope level, so it
  caches no data — reads always resolve to the live container scope, hence always
  current.
- **Per-execution data is the node execution's ephemeral I/O working set**, not a
  node field and not a cached scope: the input/output **parameter instances** for
  *one* execution (plus exec carriers like `resChan`). Loaded from the container
  scope **at node-start** (current as of node-start), worked on in **isolation**
  (keyed by execution identity, not by node name — `/<nodeName>` is what collides
  today), and committed back to the container scope at completion. Two tracks
  crossing one node get two separate working sets → no clobber; both read/commit
  the same live container scope.
- **Per-execution data comes off the node.** The exec fields (`dataPath`,
  `ExclusiveGateway.scope`, `UserTask.resChan`) leave the node object; the node
  keeps only its immutable **config** (IoSpec, data associations, properties) and
  its ADR-009 **lifetime** state.
- **A node touches data only through the role interfaces**, with the **track
  providing container-scope access**: `scope.NodeDataConsumer` (`LoadData`) on the
  way in, `scope.NodeDataProducer` on the way out (the interface a node implements
  only if it produces data). The `scope.NodeDataLoader.RegisterData(DataPath, …)`
  "node saves the path" pattern is **retired** — the execution's working-set
  identity is not a node field.
- **Scope access is direct, not event-loop-mediated.** The container scope is the
  shared source of truth, guarded on the instance (the existing `RWMutex`); the
  event loop owns *track lifecycle*, not data. The loop does not appear in this
  ADR.

## 3. Open questions (for full authoring)

- The exact per-execution scope-frame identity (track id + step? a frame handle
  the track passes to the node) and how the path namespace changes from
  `/<nodeName>`.
- The `Exec` / data-role interface signature changes (how the track hands a node
  its scope frame; whether `Exec` gains an explicit execution context).
- The in-place mutation of shared `IoSpec` / `properties` parameter structures
  (`task.LoadData` / `updateOutputs`) — a second clobber surface that per-
  execution input/output parameter copies must address.
- Serialization of concurrent commits to genuinely shared process variables
  (data objects at process scope) — whether the instance scope's `RWMutex`
  suffices or per-execution staging + commit is needed.

## 4. References

- [ADR-001 v.5 Execution Model](ADR-001-execution-model.md) — §4.7 runtime-state
  ownership / immutability invariant; §6 the flagged shared-node `RegisterData`
  race this ADR resolves.
- [ADR-009 v.1 Per-instance node graph](ADR-009-per-instance-node-graph.md) — node
  **lifetime** state ownership (sibling concern; this ADR owns **per-execution**
  data).
- [ADR-005 Gateways & Joins](ADR-005-gateways-and-joins.md) — the Parallel work
  that makes two tracks cross one node, exercising this model.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-11 | Ruslan Gabitov | Initial **seed** — reserves the ADR-010 slot and records the direction: per-execution data is track-owned (a per-execution scope frame, current at node-start, read-through to live instance scope, isolated per execution), off the node; nodes touch data only via `scope.NodeDataConsumer` / `NodeDataProducer` with the track providing scope access; `NodeDataLoader.RegisterData`'s node-held `DataPath` retired; scope access stays direct (instance `RWMutex`), not loop-mediated. Distinct from ADR-009 (node lifetime state). Not authored in full; Parallel gateway (ADR-005) takes priority. |
