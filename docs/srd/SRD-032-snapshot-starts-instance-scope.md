# SRD-032 — Snapshot start-events precompute and instance-scope encapsulation

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-06-29 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-012 v.1 §2.5 Execution layering](../design/ADR-012-execution-layering.md) |

Two behavior-preserving internal refactors in the instance/registry area, landed
together because both shrink the same overgrown surfaces:

1. **Snapshot start-events precompute (M1).** Compute the instantiating start
   triggers once, during `snapshot.New`'s single node pass, into an immutable
   `Snapshot` section — so `scanInstantiatingStarts` stops re-walking every node
   on a second O(nodes) pass.
2. **`instanceScope` encapsulation (M2).** Pull the data-plane wiring out of the
   1646-line `internal/instance/instance.go` into a small `instanceScope` value,
   closing the audit's literal §2.3 "Scope role" line and honoring one-entity-per-file.

**No public API change; no behavior change.**

---

## 1. Background & §2.3 reconciliation (verified against the code)

The 2026-06-11 architecture audit (§2.3) flagged `internal/instance/instance.go`
as an 852-line god-object and recommended **"extract `InstanceScope` to fix the
`addData` race."** That first step is **already done**:

- `scope.Scope` (`internal/scope/scope.go:27`) is a standalone type owning the
  scope map under **its own mutex** `m sync.Mutex` (`:33`); the class comment
  (`scope.go:15-19`) states no compound operation spans lock acquisitions.
- Instance no longer implements `scope.Scope` — it holds `dataPlane *scope.Scope`
  and builds it via `scope.New(inst.rootScope, inst)` (`instance.go:527`), passing
  itself as the narrow 2-method `scope.RuntimeVarsSupplier` (`RuntimeVar`
  `instance.go:1459`, `RuntimeVarNames` `:1507`). The old `addData`/`getData`
  Instance methods were removed in **SRD-007** (data-plane/frames). **The
  `addData` race is structurally gone** — Instance cannot touch the scope map
  except through `scope.Scope`'s atomic methods.

So §2.3's **bug-half is closed**. What remains is the **god-object size**:
`instance.go` has since grown to **1646 lines** (≈2× the audited size) as
SRD-022/025/027/028/029 piled the **event loop + token/track tracking** onto it.
That decomposition (the event-loop extraction) is **out of scope here** — ADR-012
v.1 §2.5 (`:138`) already names *"Splitting the `Instance` god-object (audit
2.3)"* as a deferred sibling refactor, and it relocates ADR-001's
single-goroutine ownership model, so it needs its own ADR + SRD + heavy `-race`
validation (see §7 Non-goals). This SRD does the two **safe, behavior-preserving**
pieces: the snapshot precompute and the scope-wiring encapsulation.

### 1.1 The snapshot two-pass redundancy

Instantiating start triggers are found by **two** O(nodes) passes today:

- **Pass 1 — `snapshot.New`** (`snapshot/snapshot.go:71-105`): loops every
  `p.Nodes()`, clones each into `s.Nodes`, then `wireClonedGraph` (`:125`)
  populates each clone's `Incoming()`.
- **Pass 2 — `scanInstantiatingStarts`** (`thresher/instance_starter.go:109`):
  re-walks `s.Nodes` and, per node with `len(n.Incoming()) == 0` and
  `isInstantiatingStartNode` (`:212`), inspects each `eDef` — keeping a message
  (`correlationKeyOf`, `:197`) or signal definition (others `continue`), resolving
  the Event-Based-gateway arm (`ArmFor`/`ParallelStart` probes, `:129-158`) — and
  builds an `*instanceStarter` (`:160`).

The expensive part (the scan + predicate + arm/corr-key resolution) is
**engine-agnostic** and produces, per qualifying `(node, eDef)`, exactly the
triple `(startNode, eDef, corrKey)`. Only `instanceStarter.thr` (`:24`) and its
fresh `id` are **engine-bound** — so the starter itself cannot live on the
engine-agnostic `Snapshot`, but the triple can.

---

## 2. Requirements

### Functional

- **FR-1 — Snapshot carries its instantiating starts.** `Snapshot` gains an
  immutable section `InstantiatingStarts []InstantiatingStart`, where
  `InstantiatingStart` is a plain descriptor `{ StartNode flow.Node; EventDef
  flow.EventDefinition; CorrelationKey *bpmncommon.CorrelationKey }`. It is
  populated by `snapshot.New` **after** `wireClonedGraph` (incoming-flow info only
  exists post-wiring), applying the same predicate `scanInstantiatingStarts` uses
  today. It holds **raw model descriptors only** — never a `*Thresher` or
  `*instanceStarter`.
- **FR-2 — `scanInstantiatingStarts` becomes a thin adapter.** It iterates
  `s.InstantiatingStarts` and wraps each descriptor into an `*instanceStarter`
  (binding `thr` + `foundation.GenerateID()`). The node re-scan, the
  `isInstantiatingStartNode`/`correlationKeyOf` probes, and the arm-resolution move
  into the snapshot package; the thresher keeps only the engine binding. The
  produced starters are **identical** (same start nodes, eDefs, corr-keys; ids
  stay fresh per build).
- **FR-3 — `Clone` shares the section by reference.** `InstantiatingStarts` is a
  definition-level concern read once by the Thresher at registration against the
  *template* snapshot; instances never re-scan starts. `Clone`
  (`snapshot.go:165`) shares it by reference alongside `Properties`/`CorrelationKeys`
  (the immutable header), or omits it — instances do not read it.
- **FR-4 — `instanceScope` owns the data-plane wiring.** A new
  `internal/instance/scope.go` defines an unexported `instanceScope` value owning
  `dataPlane *scope.Scope`, `rootScope scope.DataPath`, and `reader
  service.DataReader`, plus the data-plane mechanics (`loadProperties` and
  `bindEventPayload`, which call `plane.Commit` internally). `Instance` holds `sc
  instanceScope` and
  delegates. The `scope.RuntimeVarsSupplier` methods (`RuntimeVar`/`RuntimeVarNames`)
  **stay on `Instance`** — they read instance lifecycle state (`STATE`,
  `TRACKS_CNT`, `STARTED_AT`) — and `Instance` is still the supplier passed when
  the plane is built. No `instanceScope → Instance` back-reference is introduced.
- **FR-5 — Behavior preserved.** Registration, auto/manual start, supersession,
  message/signal instantiation, Event-Based start (exclusive + parallel),
  correlation, and the data plane behave exactly as before. This is an internal
  refactor.

### Non-functional

- **NFR-1 — One fewer O(nodes) pass.** Registration computes the instantiating
  starts once (in `New`) instead of twice; the thresher does O(starts) wrapping,
  not O(nodes) scanning.
- **NFR-2 — No race regression.** `go test -race ./...` stays green; the
  data-plane concurrency (the audit's `addData` class) stays fixed — a `-race`
  test pins two forked tracks committing concurrently.
- **NFR-3 — Coverage.** Diff-coverage ≥ project standard (95%, aim 100%) on every
  touched file; `make ci` green; the 18 examples still run.
- **NFR-4 — No new package cycles.** `internal/instance/snapshot` may import
  `pkg/model/events`/`msgflow` for the moved probes (no cycle — `events` does not
  import `snapshot`); `pkg/model ↛ internal` depguard stays clean.

---

## 3. Models

### 3.1 The Snapshot section (`internal/instance/snapshot/snapshot.go`)

```go
// InstantiatingStart is one resolved instantiating start trigger of a process,
// precomputed at snapshot build time: the node a born instance runs from, the
// event definition that fires it, and the correlation key (nil = name-match
// only). Raw model descriptors — engine-agnostic, no *Thresher.
type InstantiatingStart struct {
	StartNode      flow.Node
	EventDef       flow.EventDefinition
	CorrelationKey *bpmncommon.CorrelationKey
}

type Snapshot struct {
	foundation.ID
	ProcessID       string
	ProcessName     string
	Nodes           map[string]flow.Node
	Flows           map[string]*flow.SequenceFlow
	Properties      []*data.Property
	CorrelationKeys []*bpmncommon.CorrelationKey
	// InstantiatingStarts is the precomputed set of instantiating start triggers
	// (FR-1), populated in New after wiring; shared by Clone (definition-level).
	InstantiatingStarts []InstantiatingStart
}
```

The predicate/arm/corr-key logic relocates from `instance_starter.go:113-158` into
a snapshot-package helper called by `New` after `wireClonedGraph`; the structural
probes `isInstantiatingStartNode` and `correlationKeyOf` move with it (pure
`flow`/`bpmncommon` interface probes, no thresher dependency).

### 3.2 The thin thresher adapter (`pkg/thresher/instance_starter.go`)

```go
func scanInstantiatingStarts(s *snapshot.Snapshot, thr *Thresher) []*instanceStarter {
	starters := make([]*instanceStarter, 0, len(s.InstantiatingStarts))
	for _, is := range s.InstantiatingStarts {
		starters = append(starters, &instanceStarter{
			thr:       thr,
			snapshot:  s,
			startNode: is.StartNode,
			eDef:      is.EventDef,
			corrKey:   is.CorrelationKey,
			id:        foundation.GenerateID(),
		})
	}
	return starters
}
```

### 3.3 `instanceScope` (`internal/instance/scope.go`, new)

```go
// instanceScope owns the instance's data-plane wiring (the scope.Scope plane,
// its root path, and the read view), extracted from Instance to keep the role
// in one file. The scope map's lock lives in scope.Scope (the addData race is
// already fixed there); this type adds no lock of its own.
type instanceScope struct {
	plane  *scope.Scope
	root   scope.DataPath
	reader service.DataReader
}
```

`Instance` gains `sc instanceScope`; `loadProperties` and `bindEventPayload` (both
of which commit into `plane`) move onto it; `Instance` retains
`RuntimeVar`/`RuntimeVarNames` (the supplier) and passes itself when
`instanceScope` builds the plane.

---

## 4. Analysis

### 4.1 Precompute in `New`, after wiring (decided)
The predicate needs `Incoming()`, which is populated only by `wireClonedGraph`
(`snapshot.go:125`). So the precompute runs as a short post-wire pass over
`s.Nodes` (or folds into one). It cannot run inside the existing clone loop
(`:71`) because incoming flows aren't wired yet there. It is still **one build**,
not a separate registration pass.

### 4.2 Raw descriptors, not starters (decided)
`instanceStarter` binds `*Thresher` (`:24`) and calls `s.thr.resolveAndLaunch`
(`:63`); the snapshot is engine-agnostic (ADR-019 §2.3) and must not import
`pkg/thresher`. So the section stores the engine-agnostic triple; the thresher
adds the binding. This keeps the dependency direction intact.

### 4.3 `Clone` shares by reference (decided)
The instantiating-starts list is read once at `RegisterProcess` against the
template snapshot; an instance is *already* launched (it consumes a start node by
id, it does not discover starts). So the section is definition-level — it lives on
the immutable header and `Clone` shares it by reference (like `Properties`/
`CorrelationKeys`, `snapshot.go:171-172`). The descriptors point into the
template's `s.Nodes`, exactly as the starters do today, so the pointers stay
template-bound and valid (no aliasing into per-instance clones).

### 4.4 The `instanceScope` extraction is a tidy-up, not a fix (decided)
The audit framed §2.3's scope step as a race-fix; that race was already closed by
SRD-007. So M2 is **encapsulation only** — pulling the data-plane wiring out of
the 1646-line file. The `RuntimeVarsSupplier` methods stay on `Instance` because
they read lifecycle state, which avoids an `instanceScope → Instance` back-ref;
the cleaner split is "data-plane mechanics in `instanceScope`, instance-state
glue on `Instance`."

### 4.5 What stays the same (decided)
No public API changes; no new locks; the event loop, token tracking,
EventProducer delegation, message correlation, and observation all stay on
`Instance` (their decomposition is the deferred event-loop ADR). The snapshot's
existing `seExists`/`eeExists`/`instStartExists` validation booleans (`:62-64`)
are unaffected.

---

## 5. API / contract surface

**No public API change.** `Snapshot` gains an exported field
`InstantiatingStarts` and the exported descriptor type `InstantiatingStart` (the
`snapshot` package is `internal/`, so this is not part of the library's public
surface). `scanInstantiatingStarts`, `instanceScope`, and the moved probes are
unexported. `Thresher`'s registration/start API is untouched; the engine behaves
identically.

---

## 6. Test scenarios

| # | Scenario | FR | Where |
|---|---|---|---|
| T-1 | `New` precomputes `InstantiatingStarts` matching the old scan for: plain message StartEvent; signal StartEvent; instantiate ReceiveTask; non-instantiating node excluded; node-with-incoming excluded | FR-1 | `snapshot/*_test.go` |
| T-2 | Event-Based start: exclusive-start resolves each arm as the start node + arm corr-key; parallel-start keeps the gate as start node + gate corr-key | FR-1 | `snapshot/*_test.go` |
| T-3 | `scanInstantiatingStarts` output is equivalent (same start nodes / eDefs / corr-keys, ids fresh) to the pre-refactor scan — characterization test | FR-2 | `thresher/*_internal_test.go` |
| T-4 | `Clone` shares `InstantiatingStarts`; a registered process still launches event-born instances after an instance is cloned (no pointer aliasing) | FR-3 | `thresher/*_test.go` |
| T-5 | `RuntimeVar` still serves `STARTED_AT`/`STATE`/`TRACKS_CNT` and `RuntimeVarNames` lists them, through the `instanceScope` wiring | FR-4 | `instance/*_internal_test.go` |
| T-6 | Data-plane `-race`: two forked tracks `Commit` concurrently — detector clean (the addData class stays fixed) | NFR-2 | `instance/*_test.go` |
| T-7 | Existing thresher start suites (`StartProcess`/`StartLatest`/`StartVersion`, message/signal instantiation, event-based start) stay green | FR-5 | existing |

---

## 7. Milestones & non-goals

- **M1 — Snapshot start-events precompute.** FR-1/FR-2/FR-3: the `Snapshot`
  section + descriptor, the post-wire precompute in `New`, the moved probes, the
  thin `scanInstantiatingStarts`, `Clone` sharing. T-1..T-4, T-7.
- **M2 — `instanceScope` encapsulation.** FR-4: the new `scope.go`, the
  data-plane wiring moved off `Instance`, delegation kept behavior-identical.
  T-5, T-6.

**Non-goals (deferred to a future ADR + SRD):** the event-loop / token-tracking
extraction (the real god-object mass — ADR-012 §2.5) and the `track`-package split.
This SRD shrinks `instance.go` only modestly (the scope wiring); the 1646-line
file is not decomposed here.

## 8. Cross-doc

- **Implements** [ADR-012 v.1](../design/ADR-012-execution-layering.md) §2.5 —
  advances the deferred Instance-decomposition item (the scope-wiring slice; the
  event-loop split stays deferred there).
- [ADR-010 v.2](../design/ADR-010-process-data-model.md) — the data plane /
  `scope.Scope` the `instanceScope` wraps.
- [ADR-009 v.1](../design/ADR-009-per-instance-node-graph.md) — per-instance
  `Clone`; grounds the §4.3 share-by-reference decision.
- [ADR-019 v.1](../design/ADR-019-definition-versioning.md) §2.3 — the snapshot is
  the engine-agnostic isolation boundary (why the section holds raw descriptors).
- SRD-007 — closed §2.3's `addData` race (data plane / frames); number-only ref.
- SRD-024/025/026 — the Event-Based-start / signal-start semantics the precompute
  preserves; number-only refs.

## 9. Definition of Done

- [x] FR-1..FR-5 wired; covered by T-1..T-7.
- [x] `Snapshot.InstantiatingStarts` populated in `New` post-wiring; probes moved
  to the snapshot package; `scanInstantiatingStarts` is the thin adapter; `Clone`
  shares the section.
- [x] `instanceScope` owns the data-plane wiring; `Instance` delegates; no
  back-reference; `RuntimeVar`/`RuntimeVarNames` behavior unchanged.
- [x] `/check-style` clean; `/check-srd` PASS.
- [x] `make ci` green incl. diff-coverage ≥95% on touched files (NFR-3) and
  `go test -race ./...` (NFR-2); 18 examples run.
- [x] §8 cross-doc pins consistent (up/sideways, versioned); no downward ref.
- [x] §10 filled; status flipped Draft → Accepted.

## 10. Implementation summary

Landed on `feat/srd-032-snapshot-starts-scope` over master `c1319cc` in two
milestones plus the doc commit.

**Commits**

| Commit | Scope |
|---|---|
| `c721c41` | doc — SRD-032 |
| `d55162a` | M1 — snapshot start-events precompute (FR-1/2/3) |
| `d34d858` | M2 — `instanceScope` encapsulation (FR-4) |

**M1 — files**

- `internal/instance/snapshot/instantiating_starts.go` (new) — `InstantiatingStart`
  descriptor; `discoverInstantiatingStarts` (the predicate / Event-Based arm /
  correlation-key loop lifted from the thresher); the moved `isInstantiatingStartNode`
  and `correlationKeyOf` probes.
- `internal/instance/snapshot/snapshot.go` — `Snapshot.InstantiatingStarts` field;
  populated in `New` after `wireClonedGraph`; shared by reference in `Clone`.
- `pkg/thresher/instance_starter.go` — `scanInstantiatingStarts` reduced to a thin
  adapter wrapping each descriptor into an `*instanceStarter`; the two probes deleted
  (moved to the snapshot package).
- `internal/instance/snapshot/instantiating_starts_test.go` (new) — T-1 (each start
  kind / exclusions), T-2 (Event-Based arm: exclusive→arm, parallel→gate), T-4
  (`Clone` share + no-alias).

**M2 — files**

- `internal/instance/scope.go` (new) — `instanceScope{plane, reader, root}` with
  `load` (build plane + commit properties, supplier passed in — no `Instance`
  back-ref), `openFrame` (consolidates the three `scope.NewFrame` sites),
  `bindEventPayload`.
- `internal/instance/instance.go` — three data-plane fields collapsed to one
  `sc instanceScope`; `loadProperties`/`bindEventPayload` removed; call sites +
  `DataReader` delegate to `inst.sc`.
- `internal/instance/{activation.go,track.go}` — the guard frame and per-node
  execution frame now go through `inst.sc.openFrame`.

**Verification (V-results)**

- `make ci` exit 0 (tidy-check · lint 0 issues · build-all · `-race` tests ·
  diff-coverage · govulncheck) across all modules.
- Combined diff-coverage **96.0% of 124 changed lines — PASS** (min 95%):
  `snapshot.go` 100%, `track.go` 100%, `instance_starter.go` 100%,
  `instantiating_starts.go` 98.2%, `scope.go` 90.0%. The 5 uncovered lines are
  unreachable defensive error-returns (`scope.New`/`openFrame` cannot fail on a
  valid-by-construction path; the non-`EventNode` guard) — the branch ceiling.
- Behaviour preserved: 156 thresher + 180 instance tests pass unchanged
  (characterization); all 18 examples run end-to-end (exit 0).
- `/check-srd` PASS (HEAD `d34d858`).

**Deferred (named in §7 Non-goals):** the real Instance god-object decomposition
(event-loop / token-tracking extraction, ADR-012 §2.5) and the `track`-package
split — each its own future ADR + SRD.

## Open questions

None.
