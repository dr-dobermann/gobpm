# SRD-005 — Parallel Gateway (split + synchronizing AND-join)

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-06-09 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-005 v.1 Gateways & Joins](../design/ADR-005-gateways-and-joins.md) |
| Refines | [ADR-001 v.5 Execution Model](../design/ADR-001-execution-model.md); [ADR-009 v.1 Per-instance node graph](../design/ADR-009-per-instance-node-graph.md) |

This SRD lands the **Parallel (AND) gateway** of [ADR-005](../design/ADR-005-gateways-and-joins.md): the diverging **split** (activate all outgoing) and the converging **synchronizing join** (wait for one token on each incoming flow, then continue on one surviving track). It is the pilot that builds the synchronizing-join machinery (the per-instance join node owns its arrival set, written by the Instance event-loop — ADR-009 v.1; the "ride an arriving track" continuation; the `TrackMerged` producer) that the Inclusive/Complex joins will reuse. It also lands the **node-execution-contract simplification** ADR-005 §2.5 mandates — collapse to a single `Execute`, removing the now-redundant prologue/epilogue hooks. Inclusive (OR), Complex, and Event-Based gateways are **out of scope** (ADR-005 §4).

## 1. Background & motivation

### 1.1 Current state (pre-landing baseline)

> This is the **starting-point** snapshot that motivated the work (captured against master before this SRD landed); file:line refs describe the *before* state. Re-confirmed at M1 against the ADR-009/SRD-006 per-instance-node-graph baseline. What it describes — the crash on a Parallel node, the absent join accounting, the producerless `TrackMerged`, the off-by-one `String()`, the prologue/epilogue residue — is exactly what this SRD removes; see §7 for the landed result.

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
- **G3.** **Node-owned synchronizing join** (ADR-005 §2.4): the `ParallelGateway` holds its per-instance arrival state + a **per-node mutex**; the track calls `Arrive` (atomic) and acts on the answer — a non-completing arrival enters the new **`AwaitingMerge`** state and its goroutine returns (the track is retained as a record, `evAwaiting` emitted); the completing arrival first completes the join — emits `evMerged` so the loop flips the awaiting tracks to `TrackMerged` — **before** executing the node, then executes and forks. The survivor's creation lineage is left intact (convergence is recorded by the absorbed tracks' own `Consumed` entries, not by re-parenting — FR-5b). The loop keeps only awaiting/ended bookkeeping (no decision, no verdict channel).
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
| FR-3 | `exec.SynchronizingJoin` interface (embeds `NodeExecutor`) with an **atomic** `Arrive(incomingFlowID, arrivingTrackID string) (complete bool, merged []string)` guarded by the node's own mutex. `ParallelGateway` implements it: it records `arrived[incomingFlowID] = arrivingTrackID` and returns `complete ⇔ len(arrived) == len(Incoming())`; on completion `merged` = the ids of every absorbed track (all prior arrivals; the completing arrival is the survivor and is omitted) and the set clears. Ids — not `*track` — keep the contract in the model layer. Exclusive gateways / activities do **not** implement it. |
| FR-4 | In the track run loop, before executing the current node, if the node implements `exec.SynchronizingJoin` **and** `len(Incoming()) > 1`, the track calls `node.Arrive(incomingFlowID, t.ID())`. **Not complete →** the track enters `AwaitingMerge`, emits `evAwaiting`, and its goroutine **returns** (it does **not** execute the node). **Complete →** the track executes the node and continues (FR-5). |
| FR-5 | On the completing arrival the surviving track **first declares the merge** — `Arrive` returns the absorbed track ids and the survivor emits one `evMerged{ track, mergedIDs }` **before** it executes the node (ADR-005 §2.5). The loop resolves those ids against its own `tracks` map (it is the sole writer of merged tracks' state) and flips each to `TrackMerged` (token `Consumed`); the awaiting goroutines have already returned. The survivor **then** executes the join node and continues/forks via `checkFlows`. The loop applies `evAwaiting`/`evMerged` to its registry only — it makes **no** synchronization decision. |
| FR-5b | The merge does **not** alter the survivor's lineage: a token at a join has many parents but `TokenPath.ParentID` holds one, so the absorbed ids are **not** folded into the survivor's `prev` (creation lineage). Convergence is represented by each absorbed track's own path entry terminating at the join with `Consumed`. Consequently `Instance.TokenHistory()` ParentIDs form an acyclic creation tree — no track is its own ancestor. (`prev` is `[]string` of track ids; the loop owns id→track resolution.) |
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
    // each incoming flow id seen this round -> the id of the track that arrived
    // on it; the single source of truth for the count and the merge set. Fresh
    // on Clone. (mu last for fieldalignment.)
    arrived map[string]string
    mu      sync.Mutex
}

func NewParallelGateway(opts ...options.Option) (*ParallelGateway, error) { /* mirror NewExclusiveGateway */ }

// Clone gives the instance a fresh arrival set + mutex (ADR-009 / SRD-006):
// config shared by reference, state fresh, flows empty.
func (pg *ParallelGateway) Clone() flow.Node { /* Gateway.clone() + fresh arrived + mu */ }

func (pg *ParallelGateway) Exec(ctx context.Context, re renv.RuntimeEnvironment) ([]*flow.SequenceFlow, error) {
    return pg.Outgoing(), nil // all outgoing, unconditional
}

// Arrive records that arrivingTrackID reached the join on incomingFlowID and
// reports completion — atomic; safe for concurrent track callers. On the
// completing arrival it returns the ids of the absorbed tracks (every prior
// arrival; the completing one is the survivor) and clears the set.
func (pg *ParallelGateway) Arrive(incomingFlowID, arrivingTrackID string) (complete bool, merged []string) {
    pg.mu.Lock()
    defer pg.mu.Unlock()
    pg.arrived[incomingFlowID] = arrivingTrackID
    if len(pg.arrived) < len(pg.Incoming()) {
        return false, nil
    }
    for _, id := range pg.arrived {
        if id != arrivingTrackID {
            merged = append(merged, id)
        }
    }
    clear(pg.arrived)
    return true, merged
}

var _ exec.SynchronizingJoin = (*ParallelGateway)(nil)
```

```go
// internal/exec/exec.go — the synchronizing-join seam: the node owns its arrival
// state + serialization; the track calls Arrive directly (no loop round-trip).
// Ids (not *track) keep the contract in the model layer — the node never
// references the runtime track type.
type SynchronizingJoin interface {
    NodeExecutor
    Arrive(incomingFlowID, arrivingTrackID string) (complete bool, merged []string) // atomic
}
```

```go
// internal/instance — two new lifecycle events (notifications, no reply):
//   evAwaiting{track}              — a track reached a join, goroutine returned (AwaitingMerge)
//   evMerged{track, mergedIDs []string} — survivor declares the merge; the loop
//                                   resolves the ids against its own tracks map
//                                   and flips each to Merged (-> Consumed).
// trackEvent gains: mergedIDs []string. stepInfo gains: inFlow *flow.SequenceFlow.
// New track state TrackAwaitingMerge (token projection -> Alive).
// The join node owns the arrival set (flow id -> arriving track id); the loop is
// the sole writer of merged tracks' state. The survivor's prev (creation lineage)
// is NOT folded with the absorbed ids — a token at a join has many parents but
// TokenPath.ParentID holds one; convergence is carried by the absorbed tracks'
// own Consumed path entries (see FR-5b).
```

The track records the incoming flow it traversed to the join (`stepInfo.inFlow`, set in `checkFlows`) and passes its own id to `Arrive`. The arrival *state* (flow id → track id) lives on the per-instance join node (ADR-009 v.1), serialized under the node's own mutex; the absorbed-track ids ride back to the survivor in the `Arrive` return and to the loop in `evMerged`.

### 4.2 Milestones (each independently buildable + CI-green)

- **M1 — Node-execution contract.** Remove `NodePrologue`/`NodeEpilogue` (interfaces + `track.go` `runNodePrologue`/`runNodeEpilogue` calls); fold `UserTask`'s registration into `Exec` (register, then await); update `user_task_test.go`. Simplifies the executeNode path M3 modifies. (ADR-005 §2.5.)
- **M2 — Parallel split.** `ParallelGateway` type + `Exec` (all outgoing) + constructor/options + unit tests; dispatch via the existing `NodeExecutor`. Demonstrable: split into independent branches that each reach their own End.
- **M3 — Synchronizing join.** `exec.SynchronizingJoin` interface (node-owned atomic `Arrive` returning absorbed ids + per-node mutex); `ParallelGateway` arrival set (`flow id → track id`) + `Clone` (FR-5a); `stepInfo.inFlow` plumbing; the new `TrackAwaitingMerge` state + `evAwaiting`/`evMerged` events; `evMerged` carries `mergedIDs []string` which the loop resolves to flip each absorbed track to `TrackMerged`; `prev []string` (creation lineage, not folded with absorbed ids — FR-5b); `trackState.String()` fix; unit/integration tests (join fires once all arrive; non-survivors enter `TrackAwaitingMerge` (goroutine returns) then `TrackMerged`→`Consumed`; survivor continues; lineage stays acyclic; mixed N→M; `-race`).
- **M4 — Example + acceptance.** `examples/parallel-gateway/`; run it e2e (step-13a smoke); `make ci` green; fill §7; flip SRD-005 and ADR-005 → Accepted.

## 5. Verification (Definition of Done)

| # | Check | Expectation |
|---|---|---|
| V1 | Unit: `ParallelGateway.Exec` returns all outgoing flows, never errors (FR-2). | all `Outgoing()` returned. |
| V2 | Unit: `Arrive` returns `complete=true` only on the last distinct incoming flow, is atomic under the node mutex, and clears the set on fire (FR-3). | false on partial, true on full. |
| V3 | Integration: a split→join process completes; the join fires exactly once after all branches arrive; non-survivors enter `TrackAwaitingMerge` (goroutine returns) then become `TrackMerged` (token `Consumed`); the surviving track continues (FR-4/5/6). | join synchronizes; one continuation. |
| V3a | Integration: across a join `Instance.TokenHistory()` ParentIDs form an acyclic creation tree — no track is its own ancestor; the absorbed track keeps its creation parent and terminates `Consumed` (FR-5b). | lineage acyclic. |
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

Landed on `feat/parallel-gateway` in five commits (doc + four milestones; one
prerequisite engine fix surfaced by M4).

**Milestone commits**

| Commit | Milestone | What landed |
|---|---|---|
| `4389f29` | Docs | ADR-005 + this SRD. |
| `0fa2a30` | M1 — node-execution contract | Removed `exec.NodePrologue`/`NodeEpilogue` and the `track.go` hook calls; folded `UserTask` registration into `Exec` (register-then-await). (FR-9 / V8) |
| `2201aa9` | M2 — split | `ParallelGateway` + `Exec` (all outgoing) + constructor/options; `Node()` dispatch fix (also applied to `ExclusiveGateway`). (FR-1/2 / V1) |
| `e47fdca` | M3 — synchronizing join | `exec.SynchronizingJoin.Arrive(incomingFlowID, arrivingTrackID) (complete, merged []string)`; per-instance `arrived map[string]string` + mutex + `Clone`; `track.synchronize`; `TrackAwaitingMerge` + `evAwaiting`/`evMerged`; loop `applyMerged`; `stepInfo.inFlow`; `prev []string` (creation lineage, **not** folded with absorbed ids — FR-5b); `trackState.String()` off-by-one fix. (FR-3/4/5/5a/5b/6 / V2/V3/V3a/V5) |
| `e5748f2` | M4 — example | `examples/parallel-gateway/` (Start → split → 2 ServiceTasks → join → End), runs to completion, exit 0. (FR-8 / V6) |

**Prerequisite fix** (`3e10385`): `Thresher.launchInstance` cancelled the
instance context via `defer` immediately after the non-blocking `Instance.Run`,
killing every plain process before it executed. Surfaced by the M4 example;
fixed (cancel retained for teardown, not deferred) with a regression test
(`TestStartProcess_RunsToCompletion`). Outside SRD-005 scope but blocking V6.

**Key files**: `internal/exec/exec.go` (interface); `pkg/model/gateways/parallel.go` (node + Arrive + Clone); `pkg/model/gateways/exclusive.go` (Node() fix); `internal/instance/track.go` (synchronize, prev, String); `internal/instance/instance.go` (spawnForks, applyMerged, spawn wrapper); `internal/instance/event.go` (evAwaiting/evMerged, mergedIDs); `internal/instance/token.go` (AwaitingMerge → Alive).

**Verification results**

| Check | Result |
|---|---|
| V1 `TestParallelGatewayExec` | 🟢 |
| V2 `TestParallelGatewayArrive` / `…Concurrent` (-race) | 🟢 |
| V3 `TestParallelJoinSynchronizes` | 🟢 |
| V3a `TestParallelJoinLineageAcyclic` | 🟢 |
| V4 non-synchronizing merge pass-through (`synchronize` `!ok`/≤1-incoming path, exercised by every non-join flow) | 🟢 |
| V5 `TestParallelJoinMixed` | 🟢 |
| V6 `examples/parallel-gateway` `go run .` → both workers, "parallel-demo completed", exit 0 | 🟢 |
| V7 `make ci` — `-race`, diff-coverage **100 %** of touched lines (≥95 % gate), govulncheck | 🟢 |
| V8 `TestNewUserTask` / `TestUserTaskExecErrors`; no prologue/epilogue residue | 🟢 |

Design refinements made during landing (vs. the as-authored §3/§4 sketches),
reconciled back into this SRD and ADR-005: (a) `Arrive` is **id-based** (track
ids, `[]string`) so the model-layer gateway never references the runtime track
type — no opaque `any`; (b) the merge does **not** fold absorbed ids into the
survivor's `prev` (FR-5b) — that produced a cyclic `TokenPath.ParentID`.

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
| v.1 | 2026-06-09 | Ruslan Gabitov | Draft. Lands the Parallel (AND) gateway per ADR-005 v.1: split (all outgoing) + **node-owned synchronizing join** — the `ParallelGateway` holds its per-instance arrival set (ADR-009 v.1) + a per-node mutex; the track calls atomic `Arrive`; a non-completing arrival enters the new `TrackAwaitingMerge` state and its goroutine returns (track retained as a record, `evAwaiting`), the completing arrival first completes the join (emits `evMerged`; the loop flips awaiting tracks → `TrackMerged`) **before** executing the node, then executes and forks, leaving its creation lineage intact (FR-5b); `trackState.String()` fix. Also lands ADR-005 §2.5's node-execution-contract simplification (remove `NodePrologue`/`NodeEpilogue`, fold `UserTask` registration into `Exec`) as M1. Four milestones (contract → split → join → example+acceptance). Scope acyclic/single-pass; OR/Complex/Event-Based and loops/excess tokens deferred. (Reconciled on resume with the landed ADR-009/SRD-006 + ADR-001 v.5, then with the design discussion: synchronization moved fully onto the node — no loop-held map, no verdict channel, no mechanism/policy split; §1.1 grounding flagged for re-verification at M1.) |
| v.1 | 2026-06-11 | Ruslan Gabitov | **Accepted.** Landed across M1–M4 (`0fa2a30` / `2201aa9` / `e47fdca` / `e5748f2`) + a prerequisite engine fix (`3e10385`, `Thresher.launchInstance` premature context cancel). Two refinements during landing, reconciled into §1.1/§3/§7/§FR and ADR-005 v.1: (a) `Arrive` is id-based (`[]string` track ids) so the model-layer gateway needs no `*track`/`any`; (b) the merge does **not** fold absorbed ids into the survivor's `prev` (new FR-5b/V3a — folding produced a cyclic `TokenPath.ParentID`). `make ci` green; diff-coverage 100 % on touched lines; §1.1 re-verify note resolved. |
