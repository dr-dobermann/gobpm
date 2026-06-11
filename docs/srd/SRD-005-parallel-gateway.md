# SRD-005 — Parallel Gateway (split + synchronizing AND-join)

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-09 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-005 v.1 Gateways & Joins](../design/ADR-005-gateways-and-joins.md) |
| Refines | [ADR-001 v.5 Execution Model](../design/ADR-001-execution-model.md); [ADR-009 v.1 Per-instance node graph](../design/ADR-009-per-instance-node-graph.md) |

This SRD lands the **Parallel (AND) gateway** of [ADR-005](../design/ADR-005-gateways-and-joins.md): the diverging **split** (activate all outgoing) and the converging **synchronizing join** (wait for one token on each incoming flow, then continue on one surviving track). It is the pilot that builds the synchronizing-join machinery (the per-instance join node owns its arrival set, written by the Instance event-loop — ADR-009 v.1; the "ride an arriving track" continuation; the `TrackMerged` producer) that the Inclusive/Complex joins will reuse. It also lands the **node-execution-contract simplification** ADR-005 §2.5 mandates — collapse to a single `Execute`, removing the now-redundant prologue/epilogue hooks. Inclusive (OR), Complex, and Event-Based gateways are **out of scope** (ADR-005 §4).

## 1. Background & motivation

### 1.1 Current state (verified against the code)

> **Re-verify at M1.** This grounding was captured before the ADR-009 / SRD-006 per-instance-node-graph landing merged to master. The facts below still hold conceptually, but file:line references may have shifted and node types now carry `Clone()` (SRD-006) — re-confirm each line at implementation start.

- Only the **Exclusive** gateway executes (`pkg/model/gateways/exclusive.go:53` `Exec`). Dispatch is by concrete-type `Exec` via `exec.NodeExecutor` (`internal/exec/exec.go:10-18`); a node lacking it aborts track creation (`internal/instance/track.go:231-236`, `:419-425`). **A Parallel gateway in a model crashes there today.**
- The fork mechanic exists: a node's `Exec` returns activated outgoing flows; the track continues on the first, emits `evFork` for the rest, one new track each (`track.go:508-547` `checkFlows`, `instance.go:340-356` `case evFork`, `event.go:8-26`).
- There is **no join accounting**: nothing reads a node's `Incoming()` to decide whether to wait (`ADR-005 §1`). `BaseNode.Incoming()/Outgoing()` is available on any node (`pkg/model/flow/node.go:118-125`).
- `TrackMerged` (`internal/instance/track.go:81`) exists and is already projected to `TokenConsumed` (`internal/instance/token.go:86-90`) — but **has no producer**.
- `trackState.String()` (`track.go:91-104`) is **off-by-one**: it lists a phantom `"TrackWaitForInteraction"` with no matching const, so every state from index 5 (`TrackMerged`) onward prints the wrong name.
- The Instance's single event-loop goroutine owns all lifecycle mutation (`instance.go:283-369`); tracks report via `trackEvent`s on a channel, no lock on lifecycle state.
- The node-execution contract still carries optional pre/post hooks: `exec.NodePrologue` / `exec.NodeEpliogue` (`internal/exec/exec.go:20-36`), invoked around `Exec` by `track.go` (`runNodePrologue` `:550-558`, `runNodeEpilogue` `:562-570`). **Only `UserTask` implements `Prologue`** (`user_task.go:153`, registers for interaction); **nothing implements `Epilogue`**. These are the node-driven-flow-control residue ADR-005 §2.5 removes.

### 1.2 Why

Parallel split/join is core BPMN and the roadmap M1 "embedded-library MVP" target. Until it lands the engine is linear-only (plus Exclusive branching). It also establishes the synchronization seam for the rest of the gateway family.

## 2. Goals & scope

### 2.1 Goals (in scope)

- **G1.** A `ParallelGateway` node type that executes: split activates **all** outgoing flows; the gateway implements `exec.NodeExecutor`.
- **G2.** A **synchronizing-join interface** in `internal/exec`, implemented by `ParallelGateway`, distinguishing synchronizing joins from Exclusive/activity pass-through merges.
- **G3.** **Node-owned synchronizing join** (ADR-005 §2.4): the `ParallelGateway` holds its per-instance arrival state + a **per-node mutex**; the track calls `Arrive` (atomic) and acts on the answer — a non-completing arrival enters the new **`AwaitingMerge`** state and its goroutine returns (the track is retained as a record, `evAwaiting` emitted); the completing arrival first completes the join — flips the awaiting tracks to `TrackMerged` (`next` = survivor), absorbs their lineage into its `previous`, emits `evMerged` — **before** executing the node, then executes and forks. The loop keeps only awaiting/ended bookkeeping (no decision, no verdict channel).
- **G4.** The `TrackMerged` producer; the `trackState.String()` off-by-one fix.
- **G5.** A runnable `examples/parallel-gateway/` demonstrating split→join end-to-end (exit 0).
- **G6.** Collapse the node-execution contract to a single `Execute` (ADR-005 §2.5): remove the `NodePrologue`/`NodeEpilogue` hooks and fold `UserTask`'s interaction registration into its `Exec`.

### 2.2 Non-goals (explicitly deferred — ADR-005 §4)

- Inclusive (OR), Complex, Event-Based gateways; the `TokenWithdrawn` producer.
- Loops re-entering a join / excess tokens on one incoming flow (scope = **acyclic, single-pass**).
- Detecting a deadlocked join (unreachable incoming branch) — documented BPMN modeling error.
- Per-execution data flow (inputs/outputs/scope routed off node fields) — [ADR-010](../design/ADR-010-process-data-model.md) (seed). This landing keeps the existing data path; G3 (only the surviving track executes the join node) keeps the join itself clear of any per-execution clobber until ADR-010 lands. (The cross-instance shared-node race is already resolved by ADR-009.)

## 3. Requirements

### 3.1 Functional

| # | Requirement |
|---|---|
| FR-1 | `gateways.NewParallelGateway(opts...)` builds a `ParallelGateway` (embeds base `Gateway`, same options as Exclusive — `WithDirection`, base id/name/doc). It implements `exec.NodeExecutor`. |
| FR-2 | `ParallelGateway.Exec(ctx, re)` returns **all** `Outgoing()` flows verbatim — no condition evaluation, no default flow, never errors (spec §13.4.1). Drives split (1→N) and the join continuation (the survivor's outgoing) identically. |
| FR-3 | `exec.SynchronizingJoin` interface (embeds `NodeExecutor`) with an **atomic** `Arrive(incomingFlowID string) (complete bool)` guarded by the node's own mutex. `ParallelGateway` implements it: it records the incoming flow in its per-instance `arrived` set and returns `complete ⇔ len(arrived) == len(Incoming())`, clearing the set when it fires. Exclusive gateways / activities do **not** implement it. |
| FR-4 | In the track run loop, before executing the current node, if the node implements `exec.SynchronizingJoin` **and** `len(Incoming()) > 1`, the track calls `node.Arrive(incomingFlowID)`. **Not complete →** the track enters `AwaitingMerge`, emits `evAwaiting`, and its goroutine **returns** (it does **not** execute the node). **Complete →** the track executes the node and continues (FR-5). |
| FR-5 | On the completing arrival the surviving track **first completes the join** — collects the retained `AwaitingMerge` tracks for this join (instance-side, under the same per-node serialization), flips each to `TrackMerged` with `next` = itself, absorbs their lineage into its own `previous`, and emits one `evMerged{ merged, into }` — **before** it executes the node (ADR-005 §2.5). It **then** executes the join node and continues/forks via `checkFlows`. The loop applies `evAwaiting`/`evMerged` to its registry only — it makes **no** synchronization decision. |
| FR-5a | `ParallelGateway` implements `Clone() flow.Node` (ADR-009 / SRD-006): config shared by reference, the `arrived` set + mutex fresh, flows empty. The per-instance node graph guarantees one arrival set per instance. |
| FR-6 | New intermediate track state **`TrackAwaitingMerge`** (token projection: `Alive` — the token still sits at the join); a merged track ends in `TrackMerged` (token `Consumed`, already mapped `token.go:86-90`). `trackState.String()` is corrected to align with the enum (incl. the new state). |
| FR-7 | A non-synchronizing merge (Exclusive / activity, N>1 incoming) keeps today's pass-through (each arrival continues independently) — unchanged. |
| FR-8 | `examples/parallel-gateway/` (new module): Start → Parallel split → two ServiceTasks → Parallel join → End, runs to completion, exit 0. |
| FR-9 | Remove `exec.NodePrologue` and `exec.NodeEpliogue` and the `track.go` `runNodePrologue`/`runNodeEpilogue` calls; fold `UserTask.Prologue`'s registration into `UserTask.Exec` (register, then await the outcome); update `user_task_test.go`. (ADR-005 §2.5.) |

### 3.2 Non-functional

| # | Requirement |
|---|---|
| NFR-1 | Race-free: `make ci` `-race` green. Concurrent arrivals at a join are serialized by the **per-node mutex** (record → test → on-fire collect is one atomic critical section); awaiting tracks' goroutines have returned, so no goroutine stays running. |
| NFR-2 | Only the surviving track ever executes/data-loads the join node (ADR-005 §2.4 ride-an-arriving-track) — so the join node's execution is never run by two tracks at once, independent of the (ADR-009-resolved) cross-instance race. |
| NFR-3 | Diff-coverage ≥95 % (aim 100 %) on touched lines (covercheck). |
| NFR-4 | No change to non-gateway execution paths; Exclusive behaviour unchanged. |

## 4. Design & implementation plan

### 4.1 Shapes (illustrative; exact `pkg/` paths per ADR-003)

```go
// pkg/model/gateways/parallel.go — per-instance node state (ADR-009 v.1), guarded
// by the node's own mutex so concurrent track arrivals are atomic (ADR-005 §2.4).
type ParallelGateway struct {
    Gateway
    mu      sync.Mutex
    arrived map[string]bool // incoming flow ids seen this round; fresh on Clone
}

func NewParallelGateway(opts ...options.Option) (*ParallelGateway, error) { /* mirror NewExclusiveGateway */ }

// Clone gives the instance a fresh arrival set + mutex (ADR-009 / SRD-006):
// config shared by reference, state fresh, flows empty.
func (pg *ParallelGateway) Clone() flow.Node { /* Gateway.clone() + fresh arrived + mu */ }

func (pg *ParallelGateway) Exec(ctx context.Context, re renv.RuntimeEnvironment) ([]*flow.SequenceFlow, error) {
    return pg.Outgoing(), nil // all outgoing, unconditional
}

// Arrive records an incoming flow and reports completion — atomic; safe for
// concurrent track callers. Clears the set when it fires.
func (pg *ParallelGateway) Arrive(incomingFlowID string) (complete bool) {
    pg.mu.Lock()
    defer pg.mu.Unlock()
    pg.arrived[incomingFlowID] = true
    if len(pg.arrived) == len(pg.Incoming()) {
        clear(pg.arrived)
        return true
    }
    return false
}

var _ exec.SynchronizingJoin = (*ParallelGateway)(nil)
```

```go
// internal/exec/exec.go — the synchronizing-join seam: the node owns its arrival
// state + serialization; the track calls Arrive directly (no loop round-trip).
type SynchronizingJoin interface {
    NodeExecutor
    Arrive(incomingFlowID string) (complete bool) // atomic under the node's mutex
}
```

```go
// internal/instance — two new lifecycle events (notifications, no reply):
//   evAwaiting{track, join}            — a track reached a join, goroutine returned (AwaitingMerge)
//   evMerged{into *track, merged []*track} — survivor declares the merge
// trackEvent gains: node flow.Node; incoming *flow.SequenceFlow.
// New track state TrackAwaitingMerge (token projection -> Alive).
// The instance holds the awaiting *track objects per join node (internal types the
// model package can't reference); their collection + ParallelGateway.Arrive sit
// under the one per-node mutex so record/retain/collect is atomic.
```

The track records the incoming flow it traversed to the join (a field on `stepInfo`, set in `checkFlows`) so it can pass it to `Arrive`. The arrival *state* (flow ids) lives on the per-instance join node (ADR-009 v.1); the awaiting *track objects* are held instance-side and serialized under the same per-node mutex.

### 4.2 Milestones (each independently buildable + CI-green)

- **M1 — Node-execution contract.** Remove `NodePrologue`/`NodeEpilogue` (interfaces + `track.go` `runNodePrologue`/`runNodeEpilogue` calls); fold `UserTask`'s registration into `Exec` (register, then await); update `user_task_test.go`. Simplifies the executeNode path M3 modifies. (ADR-005 §2.5.)
- **M2 — Parallel split.** `ParallelGateway` type + `Exec` (all outgoing) + constructor/options + unit tests; dispatch via the existing `NodeExecutor`. Demonstrable: split into independent branches that each reach their own End.
- **M3 — Synchronizing join.** `exec.SynchronizingJoin` interface (node-owned atomic `Arrive` + per-node mutex); `ParallelGateway` arrival set + `Clone` (FR-5a); `stepInfo` arriving-flow plumbing; the new `TrackAwaitingMerge` state + `evAwaiting`/`evMerged` events; instance-side awaiting-track collection; `TrackMerged` producer (survivor flips awaiting → merged, sets `next`, absorbs lineage); `trackState.String()` fix; unit/integration tests (join fires once all arrive; non-survivors enter `TrackAwaitingMerge` (goroutine returns) then `TrackMerged`→`Consumed`; survivor continues with merged lineage; mixed N→M; `-race`).
- **M4 — Example + acceptance.** `examples/parallel-gateway/`; run it e2e (step-13a smoke); `make ci` green; fill §7; flip SRD-005 and ADR-005 → Accepted.

## 5. Verification (Definition of Done)

| # | Check | Expectation |
|---|---|---|
| V1 | Unit: `ParallelGateway.Exec` returns all outgoing flows, never errors (FR-2). | all `Outgoing()` returned. |
| V2 | Unit: `Arrive` returns `complete=true` only on the last distinct incoming flow, is atomic under the node mutex, and clears the set on fire (FR-3). | false on partial, true on full. |
| V3 | Integration: a split→join process completes; the join fires exactly once after all branches arrive; non-survivors enter `TrackAwaitingMerge` (goroutine returns) then become `TrackMerged` (token `Consumed`, `next` = survivor); the surviving track continues with the merged lineage in `previous` (FR-4/5/6). | join synchronizes; one continuation. |
| V4 | Integration: a non-synchronizing merge (Exclusive/activity, N>1 incoming) still passes each arrival through independently (FR-7). | unchanged behaviour. |
| V5 | Mixed gateway (N incoming, M outgoing): survivor executes and forks on M (FR-2 + fork). | M branches continue. |
| V6 | `examples/parallel-gateway/` runs to completion, exit 0 (FR-8; step-13a smoke). | exit 0; expected output. |
| V7 | `make ci` green — `-race` tests, diff-coverage ≥95 % on touched lines, govulncheck; Exclusive and existing examples unaffected (NFR-1/3/4). | all pass. |
| V8 | After M1: no `NodePrologue`/`NodeEpilogue` interfaces or `track.go` hook calls remain; `UserTask` still registers then awaits via `Exec`; `user_task` tests green (FR-9). | hooks gone, behaviour preserved. |

## 6. Risks & regressions

- **Single-executor join (ADR-005 §2.4 / NFR-2).** Only the surviving (completing) track executes the join node; a non-completing arrival must call `Arrive`, get `complete=false`, enter `AwaitingMerge` and let its goroutine return **without** executing the node. A test asserts non-survivors never invoke it. (The per-node mutex serializes concurrent arrivals; the cross-instance shared-node race is already resolved by ADR-009.)
- **Deadlocked join** (unreachable incoming) hangs the instance — documented modeling error, not detected (ADR-005 §4).
- **`trackState.String()` fix** must keep existing state names stable (only remove the phantom + realign) so logs/tests reading names don't break.
- **Loops** are out of scope; a join re-entered by a cycle is undefined here (ADR-005 §4).
- **Folding `UserTask.Prologue` into `Exec` (FR-9)** must preserve register-then-await ordering (register for interaction *before* awaiting the outcome) so human interaction still works; the `user_task` test must assert this.

## 7. Implementation summary

> ⚠️ TODO: filled at landing — files/lines, V-results, milestone SHAs.

## 8. References

- [ADR-005 v.1 Gateways & Joins](../design/ADR-005-gateways-and-joins.md) — the conception this lands.
- [ADR-001 v.5 Execution Model](../design/ADR-001-execution-model.md) — §4.4 fork, §4.5 join, §4.7 runtime-state ownership.
- [ADR-009 v.1 Per-instance node graph](../design/ADR-009-per-instance-node-graph.md) — the per-instance node the join's arrival set lives on; provides node `Clone()` (FR-5a).
- [bpmn-spec/semantics/gateways.md](../bpmn-spec/semantics/gateways.md) (§13.4.1), [token-flow.md](../bpmn-spec/semantics/token-flow.md).

## 9. Open questions

- None blocking. (Loop/excess-token re-arming and OR-join semantics are deferred to the next gateway revision per ADR-005 §4.)

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-09 | Ruslan Gabitov | Draft. Lands the Parallel (AND) gateway per ADR-005 v.1: split (all outgoing) + **node-owned synchronizing join** — the `ParallelGateway` holds its per-instance arrival set (ADR-009 v.1) + a per-node mutex; the track calls atomic `Arrive`; a non-completing arrival enters the new `TrackAwaitingMerge` state and its goroutine returns (track retained as a record, `evAwaiting`), the completing arrival first completes the join (flips awaiting tracks → `TrackMerged` (`next`=survivor) absorbing their lineage, emits `evMerged`) **before** executing the node, then executes and forks; `trackState.String()` fix. Also lands ADR-005 §2.5's node-execution-contract simplification (remove `NodePrologue`/`NodeEpilogue`, fold `UserTask` registration into `Exec`) as M1. Four milestones (contract → split → join → example+acceptance). Scope acyclic/single-pass; OR/Complex/Event-Based and loops/excess tokens deferred. (Reconciled on resume with the landed ADR-009/SRD-006 + ADR-001 v.5, then with the design discussion: synchronization moved fully onto the node — no loop-held map, no verdict channel, no mechanism/policy split; §1.1 grounding flagged for re-verification at M1.) |
