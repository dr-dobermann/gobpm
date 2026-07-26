# SRD-064 — Data Store port + Data Store Reference

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-07-25 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-030 v.1](../design/ADR-030-data-objects-and-store.md) §2.5–§2.6 (the `DataStore` as an engine-level infrastructure port with a default in-memory adapter; the `DataStoreReference` as a flow-scope handle whose I/O routes to the engine-global store) |
| Upstream | [SAD-001 v.1](../design/SAD-001-vision-and-architecture.md) G4 (the infrastructure-port pattern — every infrastructure concern behind an interface, default in-memory), §14.1 (the item-aware value-less rejection this reuses); [ADR-010 v.2](../design/ADR-010-process-data-model.md) (the runtime data plane + `renv.EngineRuntime` engine-service accessors this extends); [ADR-011 v.7](../design/ADR-011-process-data-flow.md) (`ItemAwareElement`, the by-name resolver, DataAssociations) |

## §1 Background

[SRD-063](SRD-063-dataobject-scope-integration.md) made a `DataObject` a **per-instance** scope-resident named container. A **`DataStore`** is the other half of BPMN §10.4.1: data that **outlives the Process instance** and is **shared across instances** within the running engine. ADR-030 §2.5 decides it is **not** a per-instance element but an **engine-level infrastructure port** — an interface with a default in-memory adapter, registered on the engine like `Repository`/`MessageBroker` (SAD-001 G4), swappable for a durable adapter later (the Persistence & State workstream).

A **`DataStoreReference`** (§10.4.1/§2.6) is the flow-scope handle: an `ItemAwareElement` carrying a `dataStoreRef` (the target store's id). It participates in DataAssociations **exactly like a DataObject**, but its backing store is **engine-global**, resolved through the runtime environment at read/write time — not per-instance scope.

Today the engine has **no** DataStore: `renv.EngineRuntime` exposes `Repository()`, `MessageBroker()`, `RuleEngine()`, … but no `DataStore()`; there is no `DataStoreReference` model element; and `task.LoadData`/`UploadData` route DataObject associations through per-instance scope only (SRD-063 FR-5). This SRD adds the port, the reference, and the engine-global routing.

## §2 Requirements

### Functional

- **FR-1 — the `DataStore` port + a `Registry`.** A new `pkg/datastore` package defines `DataStore` — read/write item-aware data **by name** (`Get`/`Put`), plus the §10.4.1 `Capacity()` / `IsUnlimited()` attributes — and a **`Registry`** (`Store(ref string) (DataStore, error)`) that resolves a store by its `dataStoreRef`. Each store has its **own capacity and backing**, so distinct refs never share state (a Process may reference many stores, §10.4.1). The default in-memory adapters live in `pkg/datastore/memstore`: `New(opts...) *Store` (mirroring `memrepo`/`membroker`) and `NewRegistry() *Registry` with `Register(ref, store)`. Registered validation follows the item-aware value rule (SAD-001 §14.1).
- **FR-2 — engine wiring, mirroring `Repository`.** `thresher.WithDataStore(ref string, store datastore.DataStore) Option` **registers** a store under `ref` (callable once per distinct store; an empty ref or nil store is rejected); `thresherConfig.dataStores` carries a `*memstore.Registry`; `renv.EngineRuntime.DataStores() datastore.Registry` exposes it to the runtime, **never nil** — absent any registration, an empty in-memory registry is wired (the `RuleEngine()` default-wiring precedent). An **unregistered ref fails loud** (`Store(ref)` errors — a DataStoreReference to an unknown store is a configuration mistake, not a silent auto-provision).
- **FR-3 — the `DataStoreReference` model element.** A new `pkg/model/data_stores` package defines `DataStoreReference` — a flow-scope `ItemAwareElement` (`EType()` of `flow.DataStoreReferenceElement`) carrying its `dataStoreRef` — so it can be **added to a `Process`/`SubProcess`** (containment: name-keyed, duplicate-guarded, **not** seeded into scope) and **bound by a `DataAssociation`** like a `DataObject`. Being engine-global, a reference is **shared across instances** (not per-instance cloned).
- **FR-4 — bidirectional association routing to the engine store.** The `DataAssociation` a `DataStoreReference` builds carries its **`dataStoreRef`** (via `data.WithDataStoreRef`); `task.LoadData`/`UploadData` branch on it (`Association.DataStoreRef() != ""`) and route I/O to the **engine-global** store — resolved from the `Registry` on the execution `Frame` (`Frame.DataStores()`, backed by `renv.DataStores()`) and keyed by the association's **item name** — never per-instance scope. An unresolvable store (no registry wired, or an unregistered ref) is a **hard error** (fail-loud), never folded into the wait/skip path:
  - **Output (Node → DataStoreReference):** the produced output (a clone) is `Put` into the resolved store under the reference's name.
  - **Input (DataStoreReference → Node):** the store's value under the reference's name fills the task input (fail-fast only when the input gates the start).
- **FR-5 — `capacity` is advisory in the in-memory adapter (ADR-030 §2.6 engine choice).** A `Put` that exceeds a nominal `Capacity()` is **not** rejected by the in-memory adapter; capacity is carried and reported, enforcement is a durable-adapter concern.
- **FR-6 — cross-instance sharing is observable.** A value written by instance A into a DataStoreReference is readable by instance B through a reference to the same store/key, within the running engine (the "outlives the instance, shared" property).

### Non-functional

- **NFR-1 — the DataStore port mirrors the existing infrastructure ports.** Same shape as `Repository`/`MessageBroker` — interface + default in-memory adapter in `pkg/datastore`, `WithDataStore` option, `EngineRuntime.DataStores()` accessor, never-nil default. No new engine-wiring idiom.
- **NFR-2 — no regression to DataObject flow (SRD-063).** The per-instance DataObject routing is untouched; the reroute only *adds* a DataStoreReference branch. The SRD-063 suites stay green.
- **NFR-3 — concurrency-safe.** The in-memory adapter is safe under concurrent instance access (`-race` clean).
- **NFR-4 — coverage.** Every touched file ≥95% diff-coverage (aim 100%); `make ci` green.

## §3 Models

### §3.1 The port (`pkg/datastore`)

```go
// DataStore is one engine-global store of item-aware data by name (BPMN
// §10.4.1, ADR-030 §2.5). Keys are opaque names.
type DataStore interface {
    Get(ctx context.Context, name string) (data.Data, bool, error)
    Put(ctx context.Context, name string, d data.Data) error
    Capacity() int    // §10.4.1 capacity (advisory in-memory, FR-5)
    IsUnlimited() bool
}

// Registry resolves a DataStore by its dataStoreRef; an unregistered ref is an
// error (fail-loud). A process may reference many stores.
type Registry interface {
    Store(ref string) (DataStore, error)
}
```

The default in-memory adapters live in `pkg/datastore/memstore`: `New(opts ...Option) *Store` (capacity unlimited unless set) and `NewRegistry() *Registry` with `Register(ref, store) error` — concrete types satisfying `datastore.DataStore` / `datastore.Registry`, mirroring `memrepo`/`membroker`.

### §3.2 The reference (`pkg/model/data_stores`)

`DataStoreReference` mirrors `DataObject`'s full shape (data_object.go) — the same `incoming`/`outgoing` association fields and `AssociateSource`/`AssociateTarget` methods (with the same `flow.AssociationSource`/`Target` + `data.FormalExpression` signatures), plus explicit `ID()`/`Name()`/`EType()`/`Docs()` disambiguating the double embedding — differing only in that it targets a `dataStoreRef` and its I/O routes to the engine store (FR-4), not scope:

```go
// DataStoreReference is the flow-scope handle to an engine-global DataStore
// (ADR-030 §2.6): an ItemAwareElement carrying the target store id.
type DataStoreReference struct {
    flow.BaseElement
    incoming     *data.Association
    outgoing     map[string]*data.Association
    data.ItemAwareElement
    dataStoreRef string
}

func New(name, dataStoreRef string, idef *data.ItemDefinition, s *data.SrcState, baseOpts ...options.Option) (*DataStoreReference, error)
func (r *DataStoreReference) EType() flow.ElementType   // flow.DataStoreReferenceElement
func (r *DataStoreReference) DataStoreRef() string      // the target store id
func (r *DataStoreReference) AssociateSource(n flow.AssociationSource, sourceIDs []string, t data.FormalExpression) error
func (r *DataStoreReference) AssociateTarget(n flow.AssociationTarget, t data.FormalExpression) error
```

The association a reference builds carries the `dataStoreRef` (`data.WithDataStoreRef` → `Association.DataStoreRef()`), which the task reroute branches on (FR-4).

### §3.3 Wiring deltas

- `pkg/renv/engineruntime.go` — add `DataStores() datastore.Registry`.
- `pkg/thresher/options.go` — `WithDataStore(ref, store)`; `thresherConfig.dataStores *memstore.Registry`; `defaultConfig` wires `memstore.NewRegistry()`. Same on `internal/enginert.Runtime` (the instance-test runtime).
- `pkg/exec/frame.go` — a `DataStores() datastore.Registry` accessor; `internal/scope.Frame` gains the field + `SetDataStores`, and `internal/instance.instanceScope` threads `renv.DataStores()` in at frame construction (the track has renv via the embedded `EngineRuntime`).
- `pkg/model/activities/task.go` — the LoadData/UploadData reroute gains a store branch (`storeFor`/`loadFromStore`/`uploadToStore` helpers) keyed on `Association.DataStoreRef()`.
- `pkg/model/data` — `Association.dataStoreRef` + `DataStoreRef()` + the `WithDataStoreRef` option.
- `pkg/model/{process,activities}` — `Process`/`SubProcess` gain a `dataStoreRefs` map + `Add` case + `DataStoreReferences()` accessor (containment).
- `pkg/model/flow` — a `DataStoreReferenceElement` element-type constant.

## §4 Analysis

### §4.1 Why an engine port, not a per-instance element (FR-1)

A DataObject's lifecycle is the instance's scope (SRD-063); a DataStore's is the **engine** (§10.4.1 "outlives the Process instance"). Modeling it per-instance would contradict the standard and force cross-instance plumbing the scope tree can't express. The engine already has the pattern — `Repository`, `MessageBroker`, `RuleEngine` are interfaces with in-memory defaults behind `WithXxx` + `EngineRuntime` accessors (SAD-001 G4). The DataStore is one more, so durability becomes a **swappable adapter**, not a reshape (ADR-030 §2.5).

### §4.2 Why route through the registry, not scope (FR-4)

SRD-063 routes DataObject I/O through per-instance scope. A DataStoreReference must reach the **shared** store, so its I/O routes through `renv.DataStores()`. To keep **all** association routing in one place (`task.LoadData`/`UploadData`, as SRD-063 established), the execution `Frame` gains a narrow `DataStores()` accessor (backed by `renv`, threaded at frame construction where the track has renv). The reroute branches on the **association's `DataStoreRef()`** (empty → scope, SRD-063; non-empty → the registry): the association carries the routing, so the reroute never needs the concrete endpoint type. Rejected: a per-endpoint type switch (needs the owning element, which the association doesn't hold) and handling references at the track level (splits routing across two layers).

## §5 API

- `datastore.DataStore` / `datastore.Registry` (new public port + resolver).
- `datastore/memstore.New` / `memstore.NewRegistry` (new default adapters).
- `data_stores.DataStoreReference` / `data_stores.New` (new public element).
- `data.WithDataStoreRef` / `Association.DataStoreRef()` (association store binding).
- `thresher.WithDataStore(ref, store)` (new option).
- `renv.EngineRuntime.DataStores()` / `exec.Frame.DataStores()` (new accessors).
- `Process.DataStoreReferences()` / `SubProcess.DataStoreReferences()` (containment).

## §6 Tests

| Test | Level | Covers |
|---|---|---|
| `TestInMemoryDataStore` | `datastore/memstore` | FR-1/FR-5 — Get/Put by name, missing-key, empty-name/nil-datum rejects, capacity advisory (over-capacity Put succeeds), IsUnlimited, `-race` |
| `TestRegistry` | `datastore/memstore` | FR-1/FR-2 — Register/resolve, unknown-ref fails loud, empty-ref/nil-store rejects, replace |
| `TestConfigSatisfiesEngineRuntime` / `TestDefaultConfigWiresEveryExtension` / `TestEveryOptionOverridesItsDefault` / `TestNilOptionValueRejected` | `thresher` | FR-2 — `WithDataStore` registers under its ref; `DataStores()` never nil; `WithDataStore(_, nil)` rejected |
| `TestDataStoreReferenceModel` | `data_stores` | FR-3 — construction/accessors (`EType`/`DataStoreRef`/`Name`), empty-name / empty-ref / nil-idef rejects |
| `TestProcessRegistersDataStoreReference` / `TestSubProcessDataStoreReferences` | `process`/`activities` | FR-3 — `Add` containment, duplicate + type-mismatch guards, `Clone` carries the (shared) references |
| `TestTaskDataStoreRouting` | `activities` | FR-4 — output writes the store, input reads it (by name via `Frame.DataStores()`); unregistered store / nil registry fail loud; absent value fails a required input |
| `TestDataStoreSharedAcrossInstances` | `thresher` | FR-6 — instance A writes, instance B reads the same store through the public engine |
| `examples/data-store` | example | the full path: two processes sharing one engine DataStore |

## §7 Milestones

- **M1 — the port + registry.** FR-1/FR-2/FR-5: `pkg/datastore` (`DataStore` + `Registry`) + `memstore` (`Store` + `Registry`); `WithDataStore(ref, store)`; `EngineRuntime.DataStores()` + default empty in-memory registry (thresher + enginert). Port/registry + option tests.
- **M2 — the reference + routing.** FR-3/FR-4: `DataStoreReference` element + `Process`/`SubProcess` containment; `flow.DataStoreReferenceElement`; `Association.dataStoreRef` + `WithDataStoreRef`; `Frame.DataStores()` threaded from renv; the task reroute (`storeFor`/`loadFromStore`/`uploadToStore`). Model + containment + activities routing tests.
- **M3 — e2e + example + docs.** FR-6 thresher e2e (shared store across instances); `examples/data-store`; CHANGELOG, data guide, conformance row 11, README EN+RU. `/check-srd`, then flip SRD-064 **and** ADR-030 → Accepted (the full data-element set; ADR-030 gets its RU twin at acceptance).

## §9 Definition of Done

- FR-1…FR-6 wired and covered by §6; the SRD-063 + data suites stay green (NFR-2).
- `make ci` green (diff-coverage ≥95% touched; `-race`; govulncheck; all modules).
- Conformance tracker row 11 advanced (DataStore/DataStoreReference ✅); CHANGELOG `[Unreleased]`; data guide note; README EN+RU.
- `/check-srd` PASS. **ADR-030 flips Draft → Accepted** with SRD-063 (the full data-element set), and gets its RU twin.

## §10 Implementation summary

Landed on branch `feat/dataobject-scope-and-datastore` (with SRD-063).

### §10.1 Stages by commit

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| doc | `690e367` | SRD-064 revised single-port → registry (this doc) | — |
| M1 | `8c28c64` | FR-1/FR-2/FR-5 — `datastore.DataStore` + `Registry`; `memstore.Store` + `Registry`; `WithDataStore(ref, store)`; `EngineRuntime.DataStores()` + default empty registry (thresher + enginert); mock regen | `datastore/memstore` (store + registry), thresher options |
| M2 | `0847f5b` | FR-3/FR-4 — `DataStoreReference` + `Process`/`SubProcess` containment; `flow.DataStoreReferenceElement`; `Association.dataStoreRef` + `WithDataStoreRef`; `Frame.DataStores()` threaded from renv; task reroute (`storeFor`/`loadFromStore`/`uploadToStore`, `loadFromScope` extracted) | reference model + associations, containment, task store round-trip + fail-loud |
| M3 | _(this landing)_ | FR-6 e2e + `examples/data-store` + docs; `/check-srd`; flips | `TestDataStoreSharedAcrossInstances` |

### §10.2 Deltas vs the draft

- **Registry, not a single port.** The original draft modeled one global store keyed by `dataStoreRef:name`. Confirming §10.4.1's unbounded multiplicity (a Process may reference many stores) drove a **registry of named stores** (each its own capacity/backing), resolved fail-loud. The association carries the **`dataStoreRef`** (not a combined key) — the reroute resolves the store from the registry and keys by the reference's item name.
- **The association carries the routing, not a per-endpoint type switch.** `Association.DataStoreRef()` (empty → scope, non-empty → registry) lets the reroute branch without the owning element; this is why the store binding rides the `Association`.
- **FR-3 containment is metadata.** A `DataStoreReference` is registrable on a `Process`/`SubProcess` (BPMN containment) but **not** seeded into scope and **not** per-instance cloned (it is engine-global) — the functional binding is the DataAssociation.
- **Defensive fill/clone error branches** in the task reroute (an input `Structure().Update` type-mismatch, a `Clone` of a value-less output) are not reachable through the frame (the input structure is permissive), so they ride the project's single-line `opErr` exclude convention rather than fake-coverage tests.

### §10.3 Backlog (out of scope)

- **Durable Data Store adapter** — the swappable-behind-the-interface upgrade (the future Persistence & State workstream); the in-memory adapter satisfies "outlives the instance" within one engine run, not across restart.
- **A custom `Registry`** option (`WithDataStoreRegistry`) — today `WithDataStore` populates the default in-memory registry; a fully custom (e.g. durable) registry is a follow-up.
- **`capacity` enforcement** — advisory in-memory (§FR-5); a durable adapter may enforce it.

## Open questions

None.
