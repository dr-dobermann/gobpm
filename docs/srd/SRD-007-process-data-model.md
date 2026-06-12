# SRD-007 — Process data model (data plane + execution frames)

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-06-12 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-010 v.1 Process Data Model](../design/ADR-010-process-data-model.md) |
| Refines | [ADR-001 v.5 Execution Model](../design/ADR-001-execution-model.md) |

This SRD lands [ADR-010](../design/ADR-010-process-data-model.md): persistent
data moves into per-instance **container scopes** owned by a dedicated
**data-plane** component with whole-operation atomicity; each node execution
works on an **execution frame** keyed by (track, node); nodes keep only
immutable data definitions plus their ADR-009 lifetime state. It retires the
architecture audit's §1.2 critical data race structurally and sheds the
Instance's scope role (audit §2.3, first step).

## 1. Background & motivation

### 1.1 Current state (verified against the code)

**Storage and locking are broken in four distinct ways:**

- `Instance.addData` (`instance.go:505-532`) splits a read-modify-write across
  lock acquisitions: `RLock` lookup → unlock → **unlocked mutation of the
  shared inner map** → `Lock` write-back. Two tracks adding data concurrently
  lose updates or fault the runtime (audit §1.2).
- `Instance.getData` (`instance.go:537-571`) reads `inst.scopes` with **no
  lock at all**, including its parent-walk loop.
- `Instance.AddData` (`instance.go:716-738`) takes `inst.m.Lock()` and then
  calls `addData`, which takes `inst.m.RLock()` — Go's `RWMutex` is not
  reentrant, so this **self-deadlocks by construction**. Its only production
  caller is `UserTask.Exec` (`user_task.go:202` `re.AddData(ut, dd...)`); it
  survives only because every test mocks the scope (`user_task_test.go:93`).
- `Instance.ExtendScope` (`instance.go:812-842`) is a TOCTOU: `RLock`
  existence check, drop lock, `Lock` insert, then `RegisterData` outside any
  lock.

**The execution data model contradicts itself:**

- A node's working data is registered under a **name-keyed** path
  (`/<process>/<nodeName>`) by `track.prepareNodeExecution` → `ExtendScope` →
  `RegisterData` (`track.go:506-516`, `instance.go:835`), and torn down by a
  **deferred `LeaveScope` that fires before `Exec` even runs** (the defer is
  inside `prepareNodeExecution`, `track.go:514`) — node reads succeed only
  because the unlocked `getData` walks up to the root after the node path is
  gone.
- Execution data lives in **mutable node fields**: `activity.dataPath`
  (`activity.go:29`, written `task.go:129`), `Event.dataPath`
  (`event.go:125`, written `start.go:135` / `end.go:158`, read
  `event.go:430`), `ExclusiveGateway.scope` (`exclusive.go:18`, written in
  `Exec` `exclusive.go:77`, read in `Find` `exclusive.go:163-171`),
  `UserTask.resChan` (`user_task.go:42`, written/read only inside `Exec`,
  `user_task.go:194/198`). ADR-009 made these per-instance; they are still
  per-*node* where they must be per-*execution* — loops and the Parallel
  fork's two-tracks-one-node crossing clobber them by construction.
- **Shared parameter instances are mutated in place**:
  `IoSpec.Parameters()` returns live `*Parameter` pointers
  (`io_spec.go:109-123`); `task.LoadData` writes into them
  (`task.go:106,114`), `task.updateOutputs` writes values and flips state
  (`task.go:222-231`), `throwEvent.LoadData` does the same to shared
  `dataInputs` (`event.go:381-392`), and `ServiceTask` mutates its
  operation's message items (`service_task.go:183-184`, `:209-213`). One set
  of instances per node, shared across executions.
- `catchEvent.UploadData(ctx)` (`event.go:263`) has the **wrong signature**
  for `scope.NodeDataProducer` (`UploadData(ctx, s Scope)`), so the track's
  producer assertion (`track.go:633`) never matches a catch event — catch-side
  data upload is dead code today.

**Everything is the Instance:** `renv.RuntimeEnvironment` embeds
`scope.Scope` (`renv.go:19`), the track passes `t.instance` to every node
`Exec` (`track.go:529`) and to `UploadData` (`track.go:638`) — the
instance-as-scope coupling the audit's §2.3 god-object finding describes.

**Runtime variables:** `GetData` intercepts a reserved path
`/<process>/RUNTIME` (`instance.go:751-753`) and synthesizes `STARTED_AT`,
`STATE`, `TRACKS_CNT` parameters on demand (`getRuntimeVar`,
`instance.go:574-618`). Read today only by `TestMonitoring`
(`instance_test.go:47-75`). Owner decision (review, 2026-06-12): the feature
**stays**, served by the data plane as a reserved read-only subtree.

### 1.2 Why

ADR-010 v.1 decides the model; the Parallel gateway (ADR-005) made concurrent
tracks routine, so every defect above sits on the execution hot path. Per the
audit triage, finding §1.2 is remediated here — structurally, not by patching
lock placement.

## 2. Goals & scope

### 2.1 Goals (in scope)

- **G1.** A dedicated data-plane component owns the container-scope tree;
  every operation is atomic under one lock (ADR-010 §2.2).
- **G2.** Every node execution works on an execution frame keyed by
  (track, node), with per-frame parameter/property instances, atomic batch
  commit on success, discard on failure (ADR-010 §2.3).
- **G3.** Nodes hold only immutable data definitions + ADR-009 lifetime
  state: the four execution-data fields and `NodeDataLoader`/`RegisterData`
  are deleted (ADR-010 §2.4).
- **G4.** The track hands each node a per-execution environment; `renv` stops
  embedding `scope.Scope`; the `Exec(ctx, renv.RuntimeEnvironment)` shape is
  unchanged (ADR-010 §2.4).
- **G5.** `-race` clean for concurrent tracks committing to one scope and for
  two tracks crossing one node; audit §1.2's code is physically deleted.
- **G6.** RUNTIME vars survive as a reserved read-only subtree of the data
  plane (owner decision §1.1).
- **G7.** A worked example exercises the persistent-data model (none does
  today); ADR-010 flips to Accepted with RU twins; roadmap/SAD/cross-doc pins
  synced.

### 2.2 Non-goals (deferred)

- Model-layer data-flow semantics — InputSet selection order, availability
  ("unavailable") gating, `DataState` semantics, `IoSpec`/`InputSet`
  structural review: the planned **data-flow ADR**. Current evaluation
  behavior is preserved as-is, only re-targeted at frames.
- `DataStore`, durable persistence, rehydration — the future Persistence &
  State ADR.
- Sub-process child scopes — the tree and `OpenScope`/`CloseScope` exist for
  them, but no sub-process lands here (roadmap WS-C4).
- Scope-change subscriptions / listeners — the observability ADR.
- The events-subsystem defects (audit 1.3/1.4/1.5) — the FIX run.

## 3. Requirements

### 3.1 Functional

| # | Requirement |
|---|---|
| FR-1 | **`internal/scope` becomes the data plane.** The `Scope` *interface* (`scope.go:31-64`) and `NodeDataLoader` (`scope.go:69-80`) are deleted; `Scope` is reborn as a **concrete struct** owning the container-scope tree: `map[DataPath]map[string]data.Data` + one `sync.Mutex`. Operations — `GetData(from DataPath, name string)`, `GetDataByID(from DataPath, id string)` (both walk parent-ward to root), `Commit(at DataPath, dd ...data.Data)` (atomic batch, the only writer), `OpenScope(DataPath)` / `CloseScope(DataPath)` (child containers; used for the root now, sub-processes later), `Root()` — each fully under the mutex; no compound operation spans acquisitions. All public methods validate every parameter (nil data, empty names, invalid paths). |
| FR-2 | **`scope.Frame`** — the per-execution working set, created by the track per node execution, identity (trackID, nodeID): per-frame **input/output `*data.Parameter` instances** built from the node's `IoSpec` definitions via `NewParameter(name, iae.Clone())` (`io_spec_obj.go:52`, `item.go:321`), per-frame **property instances** (activity/event properties — visible to this execution only, per the standard), and lookup that resolves frame-first then container walk-up. `Frame.Commit()` flushes outputs to the container scope as one `Scope.Commit` batch; `Frame.Discard()` drops everything (failure path — container scope observes nothing). |
| FR-3 | **Per-execution environment.** `renv.RuntimeEnvironment` (`renv.go:16-28`) drops the embedded `scope.Scope` and instead exposes the frame-backed data access (`GetData`/`GetDataByID` resolving frame → container; it implements `data.Source` for expression evaluation). The track constructs it per execution (instance services + frame); `Exec(ctx, renv.RuntimeEnvironment)` signatures across nodes are unchanged — the *value* becomes per-execution (today `t.instance`, `track.go:529`). |
| FR-4 | **Track hooks rework** (`track.go:494-546`): `prepareNodeExecution` creates the frame and runs the consumer role (input loading into the frame); the frame lives through `Exec`; `finalizeNodeExecution` runs the producer role and `Frame.Commit()`; failure/termination paths call `Frame.Discard()`. The Leave-before-Exec defect disappears with `ExtendScope`/`LeaveScope` themselves. |
| FR-5 | **Node migration — activities.** `task`: delete `dataPath` + `RegisterData` (`task.go:128-151`); `LoadData`/`UploadData` re-target the frame's parameter instances (today's in-place mutation of shared `IoSpec` pointers at `task.go:106-114`, `:222-231` ends). `ServiceTask`: per-execution operation message items (frame-held instances; `service_task.go:183`, `:209-213` re-targeted). `UserTask`: `resChan` becomes an `Exec`-local (written/read only inside `Exec` today, `user_task.go:194/198`); the result data goes to frame outputs committed by the track — the `re.AddData` self-deadlock path (`user_task.go:202`) is deleted with `AddData` itself. |
| FR-6 | **Node migration — events.** `Event.dataPath` + `RegisterData` deleted (`event.go:124-125`, `start.go:134-138`, `end.go:157-161`); `throwEvent.LoadData` fills frame input instances (today `event.go:381-392` mutates shared maps); `emitEvent`'s scope read (`event.go:430`) resolves via the per-execution environment; `catchEvent.UploadData` gets the producer-role signature and goes **live** (today dead: wrong signature, `event.go:263`), committing catch outputs through the frame. |
| FR-7 | **Node migration — gateways.** `ExclusiveGateway.scope` field deleted (`exclusive.go:17-20`); `Find` (`exclusive.go:159-171`) is served by the per-execution environment's `data.Source` instead of a captured node field. `ParallelGateway` unchanged — `arrived`+`mu` is ADR-009 lifetime state (`parallel.go:18-22`). |
| FR-8 | **Instance sheds the scope role.** Delete: `scopes`/`rootScope`/`runtimeScope` fields (`instance.go:101-107`), all eight `scope.Scope` methods (`instance.go:699-874`), `addData`/`getData`/`getRuntimeVar` (`instance.go:505-618`), the `scope.Scope` interface assertion (`instance.go:898`). The Instance **owns** a `*scope.Scope` created at `New`; `loadProperties` (`instance.go:196-227`) becomes a `Scope.Commit` of process properties into the root scope. |
| FR-9 | **RUNTIME reserved subtree.** The data plane serves `/<process>/RUNTIME` read-only: `STARTED_AT`, `STATE`, `TRACKS_CNT` (names per `instance.go:38-46`), fed by an instance-provided supplier so values stay live; writes to the reserved path are rejected. `TestMonitoring` (`instance_test.go:47-75`) migrates to the data plane. |
| FR-10 | **Mocks & contracts.** `.mockery.yaml` (`:8-16`): drop `Scope`/`NodeDataLoader` mocks, keep/adjust `NodeDataConsumer`/`NodeDataProducer` to their new signatures, regenerate `mockrenv` for the slimmed `RuntimeEnvironment`. `internal/exec/doc.go` §"Data management" (`doc.go:70-100`) rewritten for the frame protocol. |
| FR-11 | **Worked example.** New `examples/process-data/`: a process with properties and data objects flowing through service-task I/O across a Parallel fork — both branches read container data, write distinct results, the end event's throw data shows the committed values. Exits 0 and prints the expected data; CI builds it like the existing examples. |

### 3.2 Non-functional

| # | Requirement |
|---|---|
| NFR-1 | `-race` clean: concurrent `Commit`/`GetData` from many goroutines, and the two-tracks-one-node crossing via the Parallel fork. |
| NFR-2 | Whole-operation atomicity is testable: no interleaving can observe a partially-applied batch commit. |
| NFR-3 | No behavior change for data-less processes: existing tests and all four examples pass unmodified (except where they assert deleted API). |
| NFR-4 | diff-coverage ≥95 % on touched files (target 100 % per the project standard); `make ci` green. |
| NFR-5 | Every new public API validates all parameters with self-identifying errors (project rule); exported symbols carry doc comments. |

## 4. Design & implementation plan

### 4.1 The data plane (`internal/scope`)

```go
// Scope is the per-instance data plane: the container-scope tree and the
// single authority for persistent process data (ADR-010 §2.2).
type Scope struct {
    m      sync.Mutex
    root   DataPath
    scopes map[DataPath]map[string]data.Data
    rt     RuntimeVarsSupplier // reserved /RUNTIME subtree (FR-9)
}

func New(root DataPath, rt RuntimeVarsSupplier) (*Scope, error)
func (s *Scope) Root() DataPath
func (s *Scope) GetData(from DataPath, name string) (data.Data, error)
func (s *Scope) GetDataByID(from DataPath, id string) (data.Data, error)
func (s *Scope) Commit(at DataPath, dd ...data.Data) error
func (s *Scope) OpenScope(p DataPath) error
func (s *Scope) CloseScope(p DataPath) error
```

`DataPath` and its methods (`datapath.go`) survive unchanged — confined to
container addressing. The interface→struct rebirth keeps the package and the
name (conception leads, naming follows); nodes never see the struct — they
see the per-execution environment.

### 4.2 The frame (`internal/scope`)

```go
// Frame is the working set of one node execution by one track
// (ADR-010 §2.3): per-execution parameter and property instances,
// frame-first lookup, all-or-nothing commit.
type Frame struct {
    trackID string
    nodeID  string
    at      DataPath           // the containing container scope
    inputs  []*data.Parameter  // per-frame instances (NewParameter + IAE.Clone)
    outputs []*data.Parameter
    props   []data.Data        // node property instances
    scope   *Scope
}

func NewFrame(trackID, nodeID string, at DataPath, s *Scope) (*Frame, error)
func (f *Frame) GetData(name string) (data.Data, error)   // frame → container walk
func (f *Frame) GetDataByID(id string) (data.Data, error)
func (f *Frame) Put(dd ...data.Data) error                // node-produced values
func (f *Frame) Commit() error                            // one Scope.Commit batch
func (f *Frame) Discard()
```

Carriers that never cross the prepare/execute/finalize boundary
(`UserTask`'s response channel, the gateway's evaluation source) become
`Exec`-locals — per-execution by construction, nothing on the node. The frame
carries what crosses stage boundaries: I/O parameter instances, properties,
the `ServiceTask` operation's per-execution message items.

### 4.3 The per-execution environment

`internal/renv.RuntimeEnvironment` keeps `engrenv.EngineRuntime`,
`InstanceID()`, `EventProducer()`, `RenderRegistrator()` and replaces the
embedded `scope.Scope` with the frame-backed surface (`GetData`,
`GetDataByID`; it implements `data.Source` so expression evaluation reads
through it — replacing `ExclusiveGateway.Find`'s captured-scope pattern).
The track builds one per execution: `instance services + frame`. Mocks
regenerate; every node `Exec` body that called `re.GetDataByID(st.dataPath,…)`
now calls `re.GetDataByID(…)` — the path argument disappears with the
node-held `DataPath`.

### 4.4 Consumer/producer roles

`scope.NodeDataConsumer` / `scope.NodeDataProducer` survive with
frame-oriented signatures (exact shape pinned in M3 code review; the contract
is: consumer fills the frame's inputs from its associations, producer yields
outputs for the track to commit). `catchEvent.UploadData` aligns to the
producer signature and becomes reachable (FR-6).

### 4.5 Milestones (each = one commit, independently CI-green)

> **Ordering note.** Replacing the embedded `scope.Scope` in `renv` ripples
> through every node at once — so new types land first (unwired, M2…M3), and
> the switch-over is one atomic milestone (M4), mirroring SRD-006's M1
> all-at-once interface change.

- **M2 — data plane.** The concrete `Scope` + `RuntimeVarsSupplier` (FR-1,
  FR-9) with full unit tests: atomicity, walk-up resolution, batch commit,
  open/close, reserved-subtree read-only, parameter validation, and the
  concurrent `-race` test (NFR-1/2). Old code untouched; both coexist.
- **M3 — frame.** `scope.Frame` (FR-2) + tests: per-frame instances
  (mutating a frame's parameter leaves the node's `IoSpec` definitions and
  other frames untouched), frame-first lookup, commit batch, discard.
  Unwired.
- **M3a — id-generator concurrency fix** (discovered at M2, owner-approved
  addition): `foundation`'s id generator drove an unsynchronized
  `math/rand.Rand` and lazily initialized its package variable with a racy
  check-then-assign — concurrent model-element construction was a data
  race. Fixed (goroutine-safe top-level `rand`, eager init, `RWMutex` over
  the generator swap/fetch) with a `-race` regression test. Hard
  precondition for M4: frames instantiate parameter instances from
  concurrent tracks, so every node crossing generates ids concurrently.
- **M4 — switch-over.** `renv` slims (FR-3); track hooks rework (FR-4); node
  migration (FR-5/6/7); Instance sheds scope (FR-8); deletions
  (`NodeDataLoader`, old `Scope` interface + Instance methods, node fields,
  clone-resets that zeroed them — `activity.go:96`, `event.go:166`,
  `exclusive.go:52`, `user_task.go:151`); mocks + `.mockery.yaml` +
  `exec/doc.go` (FR-10); all existing tests migrated; `TestMonitoring` moves
  to the data plane.
- **M5 — acceptance.** `examples/process-data` (FR-11); all examples smoke;
  `make ci`; ADR-010 → Accepted + RU twin; SRD-007 §7 filled; roadmap/SAD
  sync; cross-doc pins audit (`/check-srd`).

## 5. Verification (Definition of Done)

| # | Check | Expectation |
|---|---|---|
| V1 | Unit: every `Scope` operation atomic; walk-up resolution; batch commit all-or-nothing; open/close; invalid-parameter rejection (FR-1). | green |
| V2 | `-race`: N goroutines hammering `Commit`+`GetData` on one `Scope`; no race, no lost batch (NFR-1/2). | `-race` clean |
| V3 | Frame isolation: two tracks crossing one node via a Parallel fork get distinct frames; both commit; container state consistent; node `IoSpec` definitions unmutated (FR-2, G5). | no clobber, `-race` clean |
| V4 | Frame failure: a failing node execution leaves zero trace in the container scope (FR-2). | nothing committed |
| V5 | Per-kind migration: task/ServiceTask/UserTask/Start/End/Exclusive each load-execute-commit through frames; catch-event upload **live**; UserTask result reaches the container scope through the frame (FR-5/6/7). | green |
| V6 | RUNTIME subtree serves live STARTED_AT/STATE/TRACKS_CNT, rejects writes; `TestMonitoring` migrated (FR-9). | green |
| V7 | Regression: existing suites + all four examples pass; data-less execution behavior unchanged (NFR-3). | green |
| V8 | `examples/process-data` exits 0 printing the expected committed values (FR-11). | green |
| V9 | `make ci` green — race tests, diff-coverage ≥95 % touched, govulncheck (NFR-4). | pass |
| V10 | Docs: ADR-010 Accepted + RU twin; roadmap/SAD synced; pins audited (G7). | done |

## 6. Risks & regressions

- **M4 blast radius.** The `renv` slim-down touches every node kind, the
  track, the Instance, mocks, and tests in one commit. Mitigation: M2/M3 land
  the new world fully tested first; M4 is mechanical re-targeting against
  stable contracts; the per-kind V5 tests are written *with* M4.
- **Concurrent construction beyond the data plane.** Frame instantiation
  builds model elements (parameters, item-aware elements) from concurrent
  track goroutines — every shared global in the construction path is a race
  candidate. The id generator was the confirmed instance (fixed in M3a);
  V3's `-race` crossing test is the standing guard for the rest of the
  path.
- **Clone fidelity of parameter instances.** `ItemAwareElement.Clone()`
  (`item.go:321`) must give value-independent copies for every `values.Value`
  kind a frame can carry; a shallow spot would silently re-share state. V3
  asserts definition-vs-frame independence explicitly.
- **Expression-engine contract.** `ExclusiveGateway` conditions read
  variables via `data.Source.Find` (`exclusive.go:142,159-171`); the
  environment must preserve `Find`'s resolution semantics (name lookup from
  the gateway's scope position) or conditions silently stop seeing data.
  Covered by the existing gateway tests re-targeted in M4.
- **ServiceTask operation messages.** The per-execution message instances
  must keep `Operation.Run`'s contract (`operation.go:202`); the
  `gooper`-functor path (used by both examples) is the regression canary
  (V7).
- **Behavioral note (intended):** values committed by a node become visible
  to *other* tracks only at frame commit — today's mid-execution shared-map
  visibility narrows to the standard's copy/commit semantics. No current
  test or example depends on mid-execution visibility (verified: examples
  carry no data; tests mock the scope).

## 7. Implementation summary

Landed on branch `feat/process-data-model` (off `master`).

**Milestone commits**

- `d30dd2c` — M2: the data plane (`internal/scope` `Scope` struct — landed
  under the transitional exported name `Plane`, renamed at M4 — with
  whole-operation atomicity, batch `Commit`, reserved RUNTIME subtree).
- `62dfc76` — M3: `scope.Frame` (per-frame instances via `IAE.Clone`,
  frame-first lookup, all-or-nothing commit, discard-on-failure).
- `389f075` — M3a: race-free id generation (`foundation` — top-level
  `math/rand`, eager init, `RWMutex` over the generator swap).
- `fbfc018` — M4: the atomic switch-over (legacy `Scope` interface +
  `NodeDataLoader` deleted; `renv` slimmed to the per-execution surface;
  `execEnv`; track frame lifecycle; all node kinds migrated; Instance owns
  the plane and supplies RUNTIME vars; mocks regenerated).
- `6c86620` — M5: `examples/process-data` (FR-11).

**Contract details pinned during implementation** (per §4.4 the exact role
signatures were an M3/M4 concern):

- `NodeDataConsumer.LoadData(ctx, *scope.Frame)` /
  `NodeDataProducer.UploadData(ctx, *scope.Frame)`.
- Frame resolution order: inputs → properties → puts → container walk.
  **Output instances never resolve** — they are write targets; resolving
  them would let a not-yet-filled output shadow the data meant to fill it
  at the producer stage (`updateOutputs` pulls the fill value by the
  output's own ItemDefinition id).
- A `DataInput` filled by its association flips its frame instance to
  Ready (BPMN §10.4.2) — before, nothing ever performed the flip, so the
  real input path could not complete (it existed only behind scope mocks).
- `ServiceTask` runs its operation on a per-execution clone; the result
  reaches the frame as a put keyed by the outgoing message item id. The
  pre-existing message flow was INVERTED (the output was copied into the
  operation message; the run result never reached the output) — fixed,
  with a missing output declaration no longer an exec-time error (model
  validation belongs to the data-flow ADR).
- `catchEvent.UploadData` went live (its old signature never satisfied the
  producer role); output associations still have no binding API
  (message-correlation work, WS-C3).

**Discovered and fixed beyond the plan**

- M3a (owner-approved insert): the id generator was a triple data race —
  unsynchronized `rand.Rand`, racy lazy init, unsynchronized setter.
- The legacy `Instance.AddData` self-deadlock and the
  Leave-scope-before-Exec defect died with their code (§1.1).

**Verification results**

| # | Result |
|---|---|
| V1 | 🟢 scope-package suite: atomicity, walk-up, batch all-or-nothing, open/close, reserved subtree, validation. |
| V2 | 🟢 `-race` hammer (8 writers + 8 readers, no lost commit). |
| V3 | 🟢 `TestDataCrossingParallelFork` — two forked tracks commit per-branch results; `-race` clean. |
| V4 | 🟢 `TestFrameDiscardOnFailure` — a failing execution leaves zero container trace. |
| V5 | 🟢 per-kind suites: frame-based task data flow incl. error paths; throw/catch association branches (white-box); `TestCatchEventUploadData` (live producer role); `TestExecuteNodeFailureStages`. |
| V6 | 🟢 RUNTIME subtree (reserved, read-only, live values; `TestMonitoring` via `RuntimeVar`). |
| V7 | 🟢 full suite green (450+); all pre-existing examples run. |
| V8 | 🟢 `examples/process-data` exits 0 printing both committed results. |
| V9 | 🟢 `make ci` green; diff-coverage 95.1 % of 628 changed lines (gate ≥95). |
| V10 | 🟢 ADR-010 + SRD-007 → Accepted; roadmap synced; cross-doc pins audited (no downward refs; all pinned versions exist). |

## 8. References

- [ADR-010 v.1 Process Data Model](../design/ADR-010-process-data-model.md) —
  the decision this lands.
- [ADR-001 v.5 Execution Model](../design/ADR-001-execution-model.md) — the
  two-layer runtime; the event loop stays out of the data plane.
- [ADR-009 v.1 Per-instance node graph](../design/ADR-009-per-instance-node-graph.md)
  — lifetime state stays on nodes; this SRD removes only execution data.
- [ADR-005 v.1 Gateways & Joins](../design/ADR-005-gateways-and-joins.md) —
  the Parallel fork used by V3's crossing test.
- Architecture audit 2026-06-11
  (`docs/audit/architecture-audit-2026-06-11.md`) — §1.2 (retired here),
  §2.3 (first step here).
- BPMN 2.0 §10.4 / §13.3.2 via `docs/bpmn-spec/semantics/data.md` — copy
  semantics and lifecycle-synchronous binding the frame implements.

## 9. Open questions

- None. The RUNTIME-vars disposition was decided by the owner during review
  (keep, reserved subtree — FR-9); frame identity and failure semantics are
  decided in ADR-010; the exact consumer/producer signatures are pinned at M3
  code review within the contract stated in §4.4.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-12 | Ruslan Gabitov | Draft. Lands ADR-010: `internal/scope` reborn as the concrete data plane (container tree, one mutex, whole-operation atomicity, batch `Commit`, reserved read-only RUNTIME subtree); `scope.Frame` keyed (track, node) with per-frame parameter/property instances (`NewParameter` + `IAE.Clone`), frame-first lookup, all-or-nothing commit, discard-on-failure; `renv` drops the embedded `scope.Scope` for a frame-backed per-execution environment (also the `data.Source` for expressions); track hooks create/commit/discard frames; nodes lose `dataPath`/captured scope/`RegisterData`/`NodeDataLoader` (the `AddData` self-deadlock and the dead `catchEvent.UploadData` go with them); Instance sheds its eight scope methods and owns the data plane. Five milestones: data plane → frame → atomic switch-over → acceptance with a new `examples/process-data`. |
| v.1 | 2026-06-12 | Ruslan Gabitov | **Accepted.** Landed across M2–M5 (`d30dd2c` / `62dfc76` / `389f075` / `fbfc018` / `6c86620`). Contract details pinned during landing (folded into §7): frame role signatures take `*scope.Frame`; frame resolution excludes output instances (write targets); association-filled inputs flip Ready (BPMN §10.4.2); `ServiceTask` runs a per-execution operation clone with the result as a frame put — fixing the pre-existing inverted message flow; `catchEvent.UploadData` went live. M3a inserted (id-generator data race, discovered at M2). `make ci` green; diff-coverage 95.1 % (gate ≥95). |
