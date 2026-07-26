# SRD-063 — Data Object scope integration

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-07-25 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-030 v.1](../design/ADR-030-data-objects-and-store.md) §2.1–§2.4 (the DataObject as a scope-resident named container, parent-tied lifecycle, additive coexistence, DataState-on-object) |
| Upstream | [ADR-010 v.2](../design/ADR-010-process-data-model.md) (the runtime data plane — container scopes, seeding via `Commit`, walk-up resolution, per-instance copy), [ADR-011 v.7](../design/ADR-011-process-data-flow.md) (`ItemAwareElement`, the read-by-name resolver, the commit-diff `DataChange` facts a scope-resident DataObject rides), [SAD-001 v.1](../design/SAD-001-vision-and-architecture.md) §14.1 (value-less item-aware rejection this reuses) |

## §1 Background

gobpm already **executes** Data Objects: a `DataObject` (`pkg/model/data_objects`) is wired to activity I/O by explicit **DataAssociations** and its value is filled at runtime through the frame `UploadData` / `LoadData` hooks — verified end-to-end by `examples/process-data` (a task output reaches its associated DataObject). That object-to-object flow is correct and stays.

The gap (ADR-030 §1, §2.1) is **scope-residence**. §10.4.1 makes a `DataObject` a diagram-visible variable with scope-tree visibility (parent + siblings + their children), and a DataAssociation may name "item-aware elements accessible in the current scope (DataObject, Property, Expression)" (§10.4.1/§10.4.2). But a gobpm `DataObject` today lives **outside** the scope tree: it cannot be registered on a `Process`/`SubProcess`, is never seeded into a container scope, and cannot be resolved **by name**. The scope substrate already does exactly this for `Property` (seeded into the root scope at instance start via `instanceScope.load` → `plane.Commit(root, …)`, resolved by walk-up); this SRD makes the DataObject use it.

## §2 Requirements

### Functional

- **FR-1 — a Process/SubProcess registers its Data Objects.** `Process.Add` and `SubProcess.Add` accept a `flow.DataObjectElement`, storing it in a per-container `dataObjects` map with a `DataObjects()` accessor — parallel to `properties`/`Properties()`. A duplicate DataObject name in one container is rejected (the `addNode`/`addFlow` duplicate pattern). Reserved-`/` names are already rejected by the data-element naming rule (SRD-010).
- **FR-2 — the Snapshot carries Data Objects; each instance owns a private copy.** `snapshot.Snapshot` gains a `DataObjects` slice, cloned from the process at `snapshot.New` (like `Properties` via `data.CloneProperties`), and **cloned again per instance** in `Snapshot.Clone` so no two instances share DataObject state (the FIX-016/017 per-instance isolation the Properties already have). A `DataObject.Clone` / `CloneDataObjects` provides the deep copy.
- **FR-3 — Process-level Data Objects are seeded into the root scope at instance start.** `instanceScope.load` commits the instance's DataObjects into the root scope alongside the Properties (one birth-init `Commit`, no `DataChange` facts — SRD-044 §4.4). A DataObject is `data.Data` keyed by its `Name()`, so it is then **resolvable by name** via the existing walk-up (`Frame.GetData` / `GetDataByID`) — no new resolution code.
- **FR-4 — SubProcess-level Data Objects are seeded into the child scope, disposed at close (§10.4.1 lifecycle).** When a SubProcess scope opens (`onScopeOpen`), its DataObjects are committed into the child scope (the `compScopeSeed` seam); they are visible to the sub-process and its descendants by walk-up and **disposed when the scope closes** (`completeScope` / `cancelScope` already drop the scope's data). Lifecycle = the parent scope, exactly as the standard requires.
- **FR-5 — DataObject data flow routes through scope, both directions (ADR-030 §2.3).** A DataObject is a per-instance scope variable; a `DataAssociation` binds it to a Node in either direction and the runtime reads/writes the **per-instance** DataObject resolved from the frame's scope (never mutating the shared association object):
  - **Output (Node → DataObject):** `task.UploadData` writes the produced output value into the per-instance DataObject (resolved from the frame), not into the association's shared target IAE.
  - **Input (DataObject → Node):** `task.LoadData` fills the task's DataInput from the per-instance DataObject's scope value.

  This **retires** the object side-channel and the unused `DataObject.Update()`. Per-instance isolation is **free** (the scope is per-instance), so no association-retargeting. Since `dataAssociations` are DO↔Node bindings only (declared I/O uses the `InputOutputSpecification` + frame), the Source/Target types classify the association — no extra attribute. `examples/process-data` now **registers** its result Data Objects on the process (so each instance seeds them) and **reads** them back by name from the instance handle — its observable result is unchanged (NFR-1).
- **FR-6 — DataState stays on the DataObject (engine choice, ADR-030 §2.4).** A `DataObject` retains its single `DataState` (`State()` / `UpdateState`, readiness qualifier); the standard's per-appearance state belongs to the deferred `DataObjectReference` (SAD-001 §14.1). No change to the readiness lifecycle.
- **FR-7 — validation.** A value-less DataObject is rejected at snapshot/registration (the existing SAD-001 §14.1 item-aware deviation — an `ItemDefinition`'s structure is its value). A DataObject whose name collides with a Property or another DataObject in the same container is rejected (one name-space per scope).

### Non-functional

- **NFR-1 — no regression to the shipped association flow.** The object-wired data flow and `examples/process-data` behave identically; the SRD-007…011 data suites stay green.
- **NFR-2 — per-instance isolation.** Two concurrent instances of the same process never share DataObject state (`-race` clean).
- **NFR-3 — coverage.** Every touched file ≥95% diff-coverage (aim 100%); `make ci` green, `-race` clean.

## §3 Models

### §3.1 Model deltas (`pkg/model/`)

- **`process/process.go`** — `dataObjects map[string]*dataobjects.DataObject` on `Process`; `Add` routes `flow.DataObjectElement` → `addDataObject` (duplicate-name guarded); `DataObjects() []*dataobjects.DataObject` accessor. (If the `process → data_objects` import direction is undesirable, store behind the `flow.DataNode` interface the object already implements and assert at seed time — decided at M1.)
- **`activities/subprocess.go`** — the same `dataObjects` field + `Add` case + accessor on `SubProcess`.
- **`data_objects/data_object.go`** — `Clone()` (deep copy for snapshot/instance isolation) + a `CloneDataObjects` helper (mirrors `data.CloneProperties`).

### §3.2 Runtime deltas (`internal/instance/`)

- **`snapshot/snapshot.go`** — `DataObjects []*dataobjects.DataObject` on `Snapshot`; populated at `New` (cloned from `p.DataObjects()`); re-cloned in `Clone` for per-instance isolation. (No association-retargeting — the scope route, FR-5, makes it unnecessary.)
- **`scope.go`** (`instanceScope.load`) — extend the birth-init `Commit(root, …)` batch to include the instance's Process-level DataObjects.
- **`scope_runtime.go`** (`onScopeOpen`) — seed a SubProcess's DataObjects into the freshly-opened child scope (the `compScopeSeed`/`Commit(child, …)` seam).
- **`activities/task.go`** (`UploadData`) — route a DataObject-targeting output association to `scope[DataObjectName]` (the frame-commit path) instead of into the target IAE; retire the object side-channel and the dead `DataObject.Update()`.

## §4 Analysis

### §4.1 Why reuse the Property substrate (FR-3/FR-4)

A `DataObject` and a `Property` differ in **visibility** (diagram-visible vs. hidden, §10.4.1), not in scope mechanics — both are `ItemAwareElement`s tied to a FlowElement, resolved by the same walk-up. The engine already seeds Properties into the root scope and resolves them by name; a DataObject is `data.Data` with a `Name()`, so committing it into the same scope makes it name-resolvable with **zero** new resolver code (FR-3). The only new plumbing is *registration* (FR-1), *snapshot carry + clone* (FR-2), and the *SubProcess scope-open seed* (FR-4) — the last reusing the compensation-seed seam.

### §4.2 Why route through scope (FR-5)

Today a task writes its DataObject output **straight into the DataObject's item-aware element** (`task.UploadData`), a side-channel that bypasses the scope plane; the object is **not** snapshot-carried or per-instance cloned, so one instance works but concurrent instances would share the object — a latent gap the single-instance `process-data` example never exposes. Two ways to close it once DataObjects are cloned per instance (FR-2):

- **(rejected) keep the side-channel + retarget on clone** — re-point each cloned task's association at its cloned DataObject. This needs new machinery (an `Association` target-setter, node association-accessors, a clone-time wiring pass) for little gain: per-instance cloning changes the example's **read** regardless, so "don't touch the write path" buys little.
- **(chosen) route through scope** — the task writes its output to `scope[DataObjectName]` (the frame-commit path all outputs already use); reads resolve by name. The **scope is already per-instance**, so isolation is free — no retargeting. It also retires the dead `Update()` and is closer to §10.4.2 (associations target scope-accessible elements). The cost is touching one shipped write path, covered by the example smoke + the `TestDataObjectScopeE2E` canary (producer writes → consumer reads one agreed value).

## §6 Tests

| Test | Level | Covers |
|---|---|---|
| `TestProcessRegistersDataObject` | model (`process`) | FR-1 — `Add` accepts a DataObject, duplicate name rejected, `DataObjects()` returns it |
| `TestSubProcessDataObjects` | model (`activities`) | FR-1 — same on SubProcess (register/list, duplicate reject, DataObjectElement type-mismatch guard) + FR-2 SubProcess clone-isolation |
| `TestSnapshotClonesDataObjects` | instance (`snapshot`) | FR-2 — snapshot carries DataObjects; two clones own private copies |
| `TestCloneDataObjects` / `TestDataObjectClone` | model (`data_objects`) | FR-2 — the deep cloner (happy / nil / nil-value failure) |
| `TestProcessDataObjectSeeding` | instance | FR-3 — a Process DataObject seeded at start is read by name via a frame walk-up |
| `TestSubProcessDataObjectSeeding` | instance | FR-4 — a SubProcess DataObject seeded at scope-open is read by name inside the scope (disposal at close is the existing `completeScope`/`cancelScope` teardown — the seeded object lives in the child scope that is dropped) |
| `TestSeedDataObjects` | instance | FR-4 — the seed helper: non-host no-op + Commit-failure wrapped, not dropped |
| `TestDataObjectScopeE2E` / `TestSubProcessDataObjectE2E` | thresher | FR-5 — a task output association fills the object the scope resolves by name (producer writes → consumer reads one agreed value), Process-level and SubProcess-level, through the public engine |
| `TestTaskDataErrorPaths` (DataObject cases) | model (`activities`) | FR-5 — the reroute error paths: unseeded-object resolve failure, type-mismatched write |
| `TestItemAwareElementName` / `TestAssociationNames` | model (`data`) | FR-5 — the by-name resolution surface: `Name`/`SetName`/clone-carry, `TargetName`/`SourceNames` |
| `TestSubProcessDataObjects` clone subtest + `TestSnapshotClonesDataObjects` | model/`-race` | NFR-2 — two instances don't share DataObject state |
| `examples/process-data` | example | NFR-1 — the observable branch results are unchanged |

## §7 Milestones

- **M1 — model + snapshot (Process-level).** FR-1/FR-2 for the Process: registration on `Process`, `DataObjects()` accessor, `DataObject.Clone`/`CloneDataObjects`, `Snapshot.DataObjects` + per-instance clone. Model + instance-clone tests. (SubProcess registration rides M2 with the child-scope seeding — it needs the `flow.ElementsContainer` + a clone-capable interface, so it groups naturally with `onScopeOpen`.)
- **M2 — scope routing + seeding + resolution.** FR-3/FR-4/FR-5: seed Process DataObjects at `instanceScope.load`; SubProcess registration + seed at `onScopeOpen`, disposed at close; **route task DataObject writes through `scope[name]`** (retire the side-channel + dead `Update()`); verify by-name resolution and association/scope agreement.
- **M3 — e2e + example + docs.** The thresher e2e; a runnable example (or an addition to `process-data` showing by-name resolution); CHANGELOG, the data guide, conformance row 11, README EN+RU. `/check-srd`, then flip Draft → Accepted.

## §9 Definition of Done

- FR-1…FR-7 wired and covered by §6; `examples/process-data` behaves identically (NFR-1) — it registers its result Data Objects and reads them back by name; the SRD-007…011 suites green.
- `make ci` green (diff-coverage ≥95% touched; `-race`; govulncheck; all modules).
- Conformance tracker row 11 advanced (DataObject scope integration ✅); CHANGELOG `[Unreleased]`; data guide note; README EN+RU.
- `/check-srd` PASS. ADR-030 stays Draft until SRD-068 (Data Store) also lands, then flips Accepted with the full data-element set.

## §10 Implementation summary

Landed on branch `feat/dataobject-scope-and-datastore` in three stages.

### §10.1 Stages by commit

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| M1 | `3c35208` | FR-1/FR-2 Process-level — `Process.dataObjects` + `Add`→`addDataObject` + `DataObjects()`; `DataObject.Clone`/`CloneDataObjects`/`EType`; `Snapshot.DataObjects` + per-instance re-clone (`cloneProcessData` extracted for gocyclo) | model + snapshot clone (happy/nil/error) |
| M2a | `ab2dfc0` | FR-3/FR-5 — root-scope seed in `instanceScope.load`; bidirectional scope routing in `task.LoadData`/`UploadData` resolved **by name**; `ItemAwareElement` gains a real `name` (Option B) + `SetName`; `Association.TargetName`/`SourceNames`; `Frame.GetData(name)`; `examples/process-data` registers + reads by name | activities I/O + error paths, data-binding accessors, thresher e2e `TestDataObjectScopeE2E` |
| M2b | `7f89ffc` | FR-4 — `SubProcess.dataObjects` + `Add`/`DataObjects`/`Clone`; `onScopeOpen` seeds via the `seedDataObjects` helper; per-instance isolation | activities Add/dup/mismatch/clone, instance seeding (sub-process + process + helper), thresher e2e `TestSubProcessDataObjectE2E` |

### §10.2 Deltas vs the draft

- **FR-5 resolves by name, not by id (Option B).** A DataObject and its bound param share one `ItemDefinition` id (§10.4.2 type-match), so `GetDataByID` is ambiguous. Resolved: the `ItemAwareElement` carries a real optional `name` (a DataObject names its IAE after itself); `Name()` falls back to the id when unset (backward-compatible, caller-audited — no site depends on the old id return). Associations expose `TargetName()`/`SourceNames()`; the reroute resolves the per-instance DataObject via `Frame.GetData(name)`.
- **FR-4 needed the full SubProcess plumbing, not just the seed.** `flow.ElementsContainer` accepts only nodes and sequence flows, so the embedded Sub-Process could not hold a DataObject at all — M2b adds the store/Add/Clone/accessor (mirroring Process). Seeding was extracted to a testable `seedDataObjects` helper (the compensation-seed seam analogue).
- **FR-7 is satisfied by existing machinery.** The name-collision guard lives in `addDataObject` (Process + SubProcess); a value-less DataObject is rejected at snapshot (its `Clone` fails on the nil value) — no dedicated validator was added.
- **`examples/process-data`'s process definition did change** (it now registers the result Data Objects); the draft's "keeps its definition" was corrected. Its observable result is unchanged (NFR-1).

### §10.3 Backlog (out of scope)

- A **transformation/assignment** on a DataObject association (§4.2) — the current uses are plain value copies; a mapping expression on the association is a follow-up.
- The **`DataStore` port** (ADR-030 §2.5/§2.6) → **SRD-068**; ADR-030 flips `Accepted` when it lands (this SRD's §9 gate).

## Open questions

None.
