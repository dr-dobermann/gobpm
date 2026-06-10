# SRD-006 — Per-instance node graph (cloning + node-owned state)

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-10 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-009 v.1 Per-instance node graph](../design/ADR-009-per-instance-node-graph.md) |
| Refines | [ADR-001 v.4 Execution Model](../design/ADR-001-execution-model.md) |

This SRD lands [ADR-009](../design/ADR-009-per-instance-node-graph.md): each Instance **clones** the immutable process node-graph into its own private graph, and nodes hold their **per-instance runtime state** directly. It removes the shared-node data race and gives synchronizing gateways / timers / correlation a home on the node. It is the foundation the Parallel-gateway work (ADR-005) resumes on.

## 1. Background & motivation

### 1.1 Current state (verified against the code)

- A **snapshot** shares the process's node pointers — `snapshot.go:51-52` `s.Nodes[n.ID()] = n` (no copy) — and one snapshot per process is reused for **every** instance (`thresher` `StartProcess` → `launchInstance(s)`). So all instances of a process share the **same node objects**.
- `instance.New` keeps the shared snapshot (`instance.go:151` `s: s`); `createTracks` iterates `inst.s.Nodes` (`instance.go:427`) and `newTrack(n, …)` runs the **shared** node; forks resolve the next node via the **flow back-pointer** `f.Target().Node()` (`instance.go:342`, `track.go:527`), not via `s.Nodes`.
- **Execution mutates fields on the shared node** (the race ADR-001 §4.7 / ADR-005 flagged): `Event.dataPath` (`event.go:125`; written by `RegisterData` — Start `start.go:125`, End `end.go:149` — via `ExtendScope`→`RegisterData` `instance.go:777`), `activity.dataPath` (`activity.go:29`; `task.go:119`, covers Service+User), `UserTask.resChan` (`user_task.go:43/175`), `ExclusiveGateway.scope` (`exclusive.go:18/59`). Under `-race` (on by default — `Makefile:117/119`) two concurrent instances over one node would race.
- A deeper sharer: `ServiceTask.operation` (`*service.Operation`, `service_task.go:36`) has its message Items mutated at exec (`service_task.go:170-198`) — sharing it across instances re-introduces a race even after the fields above are isolated.
- Cloning machinery precedent exists for *data*: `data.ItemAwareElement.Clone()` (`item.go:321`) — same ID, independent value — and `flow.EventDefCloner.CloneEventDefinition` (`events.go:65`, used in `throwEvent.emitEvent` `event.go:410`). No node/flow `Clone()` exists.

### 1.2 Why

Per-instance runtime state (ADR-009) is the foundation for synchronizing joins (ADR-005), timers, and correlation, and it eliminates the shared-node race. Without it, none of that has a clean home and the race stands.

## 2. Goals & scope

### 2.1 Goals (in scope)

- **G1.** Each Instance owns a **cloned** node graph; the snapshot stays the shared immutable template.
- **G2.** Every concrete node type has a `Clone()` (shares immutable config by reference, fresh runtime state, empty flows); the cloned graph's topology is rewired so `Source()/Target()` resolve to **cloned** nodes.
- **G3.** The exec-mutated fields become **per-instance**: `Event.dataPath`, `activity.dataPath`, `UserTask.resChan`, `ExclusiveGateway.scope`, and `ServiceTask`'s exec-mutated operation message state.
- **G4.** The shared-node data race is gone — `-race` clean for ≥2 concurrent instances of one process.
- **G5.** Reconcile **ADR-001 §4.7** (runtime-state ownership decided; durable persistence still deferred) and update the **roadmap** (add ADR-009).

### 2.2 Non-goals (deferred)

- Durable persistence / serialization / rehydration / restart recovery — the future Persistence & State ADR (ADR-009 §3).
- The gateway join itself (ADR-005 / SRD-005, resumes after this).
- New node types (Script/Send/Receive/BoundaryEvent) — only the 5 existing executable types get `Clone()`.

## 3. Requirements

### 3.1 Functional

| # | Requirement |
|---|---|
| FR-1 | `snapshot.(*Snapshot).Clone()` returns a **per-instance `*Snapshot`** with cloned `Nodes` + relinked `Flows` (immutable header — `ProcessID`/`ProcessName`/`Properties` — shared by reference). `instance.New` calls it and holds **its own cloned snapshot** in place of the shared one; the thresher's snapshot stays the shared **template** and is never mutated. `createTracks` already iterates `inst.s.Nodes`, so it now iterates the **cloned** nodes with no further change. |
| FR-2 | Each of the **5 concrete node types** — `events.StartEvent`, `events.EndEvent`, `activities.ServiceTask`, `activities.UserTask`, `gateways.ExclusiveGateway` — implements `Clone() flow.Node`, reproducing embedded-parent fields (`Event`/`catchEvent`/`throwEvent`, `activity`/`task`, `Gateway`, `flow.BaseNode`): **immutable config shared by reference**, **runtime state fresh**, **flows empty** (rewired by FR-4). |
| FR-3 | The runtime-mutated fields are **per-instance** (fresh/zero on clone): `Event.dataPath` (`event.go:125`), `activity.dataPath` (`activity.go:29`), `UserTask.resChan` (`user_task.go:43`), `ExclusiveGateway.scope` (`exclusive.go:18`). |
| FR-4 | A **`flow`-package clone helper** rebuilds topology between cloned nodes: for each original `SequenceFlow`, create a cloned flow that **preserves the flow id and condition**, sets `source`/`target` to the **cloned** endpoints, and wires it into both cloned nodes' flow maps. It must **not** regenerate the id and **not** attempt container insertion (clones carry no shared container). (`flow.Link`→`connect` `sequenceflow.go:157` is the wiring reference; `flows` is unexported so the rewire lives in package `flow`.) |
| FR-5 | `ServiceTask.Clone()` yields a **per-instance** operation so exec-mutated message state (`service_task.go:170-198`) is not shared across instances — clone `*service.Operation` (share its immutable definition, fresh message carriers). |
| FR-6 | Cloned nodes carry **no cross-instance back-reference** — the `flow.BaseElement.container` (`element.go:95`) is cleared/instance-local so two instances are fully independent and `Link`-style container inserts don't fire on clones. |
| FR-7 | Event-definition registration stays unambiguous when clones reuse definition ids — confirm the EventProducer keys subscriptions by `(processor, defID)` (not `defID` alone); `track.checkNodeType`→`RegisterEvent` (`track.go:285`, `instance.go:601`) / unregister by `eDef.ID()` (`track.go:582`). If keyed by `defID` alone, fix it. |

### 3.2 Non-functional

| # | Requirement |
|---|---|
| NFR-1 | `-race` clean for ≥2 concurrent instances of one process executing over the previously-shared nodes (the §3 race eliminated). |
| NFR-2 | Per-instance startup is O(nodes + flows); immutable config (definitions, operations' templates, conditions) shared by reference — memory stays low. |
| NFR-3 | No behavior change to single-instance execution; all existing tests + all three examples still pass. |
| NFR-4 | diff-coverage ≥80 % (aim 100 %) on touched files. |

## 4. Design & implementation plan

### 4.1 Clone mechanism

- **Per-type `Clone()`** (in each node's own package, so it can touch its fields): copy config fields **by reference** (event definitions, operation template, conditions, direction, default-flow, ids, names), allocate **fresh** runtime-state fields (FR-3), and start with **empty** `BaseNode.flows`. Embedded parents expose internal clone helpers (`Event.clone`, `activity.clone`, `task.clone`, `Gateway.clone`, `BaseNode.cloneShell`).
- **`Snapshot.Clone()` mirrors `snapshot.New`.** `snapshot.New` builds the template with two independent loops: nodes (`snapshot.go:51-52`) and flows (`snapshot.go:87-89`) into id-keyed maps. `Clone()` reuses that exact shape on a built snapshot: iterate `s.Nodes` → `n.Clone()` into an `id→clone` map; iterate `s.Flows` → recreate each edge **between the clones** (preserve id + condition, no container insert); copy the immutable header (`ProcessID`/`ProcessName`/`Properties`) by reference. Because the snapshot already enumerates every node and flow, **no graph traversal is needed** — the edge recreation is a small package-`flow` helper (`flows` is unexported). Returns a new `*Snapshot`.
- **Hook:** `instance.New` calls `s.Clone()` once at start and stores the returned per-instance `*Snapshot` as `inst.s`; the thresher's snapshot stays the shared template. `createTracks` (`instance.go:427`) and forks already read `inst.s.Nodes` / the flow graph, so they transparently use the clone.

### 4.2 State-field migration

The four fields in FR-3 already live on the node; the only change is that they are now **per-instance** because the node is per-instance — clone zeroes them, exec writes them on the instance's own node. No signature changes; the §3 race is gone because the writes target a private node.

### 4.3 `service.Operation` per-instance

`ServiceTask.Clone()` clones the operation (share the immutable operation definition; fresh message carriers) so `loadInputMessage`/`uploadOutputMessage` mutate per-instance state. (If `service.Operation` cloning proves entangled, the fallback is to route message data through the per-instance scope instead of the operation — decided in M2 with evidence.)

### 4.4 Milestones (each independently buildable + CI-green)

> **Ordering note.** `Snapshot.Clone()` iterates `s.Nodes` calling each node's `Clone()`, and adding `Clone()` to the `flow.Node` interface forces every implementer to exist at once — so all clone code lands as one atomic milestone, then the wiring, then acceptance.

- **M1 — `Clone()` across the model.** Add `Clone() flow.Node` to the `flow.Node` interface; implement on `BaseNode` (shell: id/name, fresh empty flows, no container), the embedded parents (`Event`/`catchEvent`/`throwEvent`, `activity`/`task`, `Gateway`), and the 5 concrete node types — config shared by reference, the four exec-mutated fields fresh, `ServiceTask` per-instance operation (FR-2/3/5/6); add the package-`flow` edge-clone helper (preserve id/condition, no container insert, FR-4); regenerate mocks. Per-type + helper unit tests (clone independent, config shared, state zeroed, container cleared). Build green; not yet wired into execution.
- **M2 — wire `Snapshot.Clone()` + `instance.New`.** `snapshot.(*Snapshot).Clone()` mirroring `snapshot.New`'s node-loop/flow-loop (clone nodes via `Clone()`, relink flows via the edge helper, share immutable header); `instance.New` swaps the shared snapshot for its clone (FR-1); confirm/fix event-def `(processor, defID)` keying (FR-7); **`-race` independence test** — two concurrent instances of one process, no race + independent `dataPath`/operation/`scope`/`resChan`; all examples run.
- **M3 — acceptance.** `make ci` green (esp. `-race`); examples smoke; reconcile **ADR-001 §4.7** + update the **roadmap**; flip **ADR-009 + SRD-006 → Accepted**.

## 5. Verification (Definition of Done)

| # | Check | Expectation |
|---|---|---|
| V1 | Unit: `Snapshot.Clone()` on a small snapshot yields an independent `*Snapshot` (cloned nodes; `Source()/Target()` point at clones; flow ids + conditions preserved; header shared) (FR-1/FR-2/FR-4). | independent, id-stable. |
| V2 | Unit: each of the 5 node `Clone()`s shares config by reference and zeroes the FR-3 state fields; flows empty pre-rewire (FR-2/FR-3). | config shared, state fresh. |
| V3 | Unit: `ServiceTask` clone has a per-instance operation; exec on one clone doesn't mutate another's message state (FR-5). | no shared operation mutation. |
| V4 | Integration: two concurrent instances of one process run under `-race` with **no race** and independent node state (FR-1/NFR-1). | `-race` clean. |
| V5 | No regression: existing instance/snapshot/event tests pass; single-instance behavior unchanged; all three examples exit 0 (NFR-3). | all green. |
| V6 | `make ci` green — `-race`, diff-coverage ≥80 % touched, govulncheck (NFR-4). | pass. |
| V7 | Docs: ADR-001 §4.7 reconciled; roadmap lists ADR-009; ADR-009 + SRD-006 flipped Accepted (G5). | done. |

## 6. Risks & regressions

- **`service.Operation` entanglement (FR-5).** Its message Items are exec-mutated; if a clean per-instance clone is hard, fall back to routing message data through per-instance scope (decided in M2 with evidence). A V3 test guards it.
- **`container` back-pointer (FR-6).** A cloned node still pointing at the original Process container, or a rewire that triggers `Container().Add`, can fail validation (`sequenceflow.go:122-128/176`). The rewire must skip container insertion and clones must not retain a foreign container.
- **Unexported `flows` (FR-4).** The rewire must live in package `flow` (or use `AddFlow`); a clone outside the package can't rebuild the map directly.
- **Event-def id registration (FR-7).** If the EventProducer keys by `defID` alone, two instances reusing a definition id would collide; M3 confirms `(processor, defID)` keying or fixes it.
- **Clone-completeness tax.** A new node type must implement `Clone()` correctly or silently share state — documented as the standing rule (ADR-009 §3); a lint/test could guard it later.

## 7. Implementation summary

> ⚠️ TODO: filled at landing — files/lines, V-results, milestone SHAs.

## 8. References

- [ADR-009 v.1 Per-instance node graph](../design/ADR-009-per-instance-node-graph.md) — the decision this lands.
- [ADR-001 v.4 Execution Model](../design/ADR-001-execution-model.md) — §4.7 (runtime-state ownership, reconciled here; durable persistence still deferred); the single event-loop writer.

## 9. Open questions

- `service.Operation` per-instance approach (clone vs. scope-routing) is decided in **M2** with evidence (FR-5) — not blocking the spec.
- EventProducer keying (FR-7) is confirmed/fixed in **M3** — not blocking the spec.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-10 | Ruslan Gabitov | Draft. Lands ADR-009: per-instance cloned node graph + node-owned runtime state. `snapshot.(*Snapshot).Clone()` (mirroring `snapshot.New`'s node-loop/flow-loop) returns a per-instance `*Snapshot`; `instance.New` swaps the shared snapshot for its clone. 5 node types get `Clone()` (config shared, state fresh; flows relinked between clones by a package-`flow` edge helper that preserves id/condition and skips container insert); the four exec-mutated fields (`Event.dataPath`, `activity.dataPath`, `UserTask.resChan`, `ExclusiveGateway.scope`) plus `ServiceTask`'s operation message state become per-instance; `-race` proves the shared-node race gone. Reconciles ADR-001 §4.7 + roadmap. Three milestones (`Clone()` across the model → wire `Snapshot.Clone()` + instance.New + race test → acceptance). |
