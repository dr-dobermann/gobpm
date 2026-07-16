# SRD-049 — Embedded Sub-Process: nested scopes in the instance

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-07-16 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-023 v.1](../design/ADR-023-sub-process-and-call-activity.md) §2.1–§2.6, §2.9 (the embedded slice; Call Activity is the next slice); GitHub epic #85 (part) |
| Upstream | [ADR-001 v.6](../design/ADR-001-execution-model.md) (the single-writer loop extended with scope accounting), [ADR-010 v.2](../design/ADR-010-process-data-model.md) §2.2 (the container-scope tree activated), [ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md) (the interruption mechanism applied to composite hosts), [ADR-006 v.3](../design/ADR-006-events-and-subscriptions.md) §2.6 (the error scope-chain realized), [ADR-013 v.2](../design/ADR-013-observability.md) (the fact stream extended with a scope kind) |
| Refines | — |

## §1 Background

ADR-023 v.1 decides composition on one concept — the execution scope as a
tree inside the instance — and slices the landing: the embedded Sub-Process
first. The code today is flat in exactly three places, each with a seam
already waiting:

- **Model**: only type constants exist (`flow.SubProcessActivity`,
  `pkg/model/flow/activity.go:11-13`); `flow.Container` is an interface
  whose doc names Sub-Process as a container kind
  (`pkg/model/flow/element.go:66-87`), with `Process` its only
  implementation. `SequenceFlow.Validate` already enforces the
  same-container rule (`sequenceflow.go:257-287`) — nested graphs validate
  for free once a second container exists. `Process.Validate` runs a
  **per-node `Validate()` hook** (`process/process.go:245-254`) — a
  SubProcess implementing it gets recursive validation with zero changes
  to Process.
- **Snapshot**: `snapshot.New` walks one flat node map
  (`snapshot/snapshot.go:44-169`) — cloning, wiring, the completeness
  check, `discoverInstantiatingStarts` and `hasConditionals` all assume no
  nesting; `Snapshot.Clone`/`wireClonedGraph` rebind boundaries/defaults
  on the flat graph only.
- **Runtime**: the loop's registries are scope-blind (`loop.go:19-71`),
  completion is `active == 0`, `Terminate` stops the whole instance
  (`end.go:141-151` → `evTerminate` → `stopAll`), the error catch is
  single-scope (`applyFailed`/`matchErrorBoundary`), and every frame opens
  at the plane root (`internal/instance/scope.go:80-84`). Meanwhile the
  data plane is **already a tree**: `scope.Scope` has
  `OpenScope`/`CloseScope` and parent-ward resolution
  (`internal/scope/scope.go:273-305, 357-393`), `Frame` carries its
  container path (`frame.go` `at`), and `Instance.New` reserves a
  `parentRoot` parameter (`instance.go:100-107`).

This SRD lands the embedded Sub-Process end-to-end on those seams: the
container-node model with the ADR-023 §2.3 validated shapes, the recursive
snapshot, the scope-aware loop (open/seed/park/drain/close), scope-cancel
(boundary-on-composite + the scoped Terminate), and the error scope-chain.

## §2 Requirements

### Functional — model layer

- **FR-1 — `flow` container concern in one file** (new
  `pkg/model/flow/container.go`): the existing `flow.Container`
  **interface moves here** from `element.go` (a pure move — no API
  change; `element.go` keeps `Element`/`ElementType`/`BaseElement`),
  joined by its first embeddable implementation **`ElementsContainer`** —
  the graph-holding core (`nodes`/`flows` maps, `Add`/`Remove`/`Elements`,
  `Nodes()`/`Flows()` accessors, a `validateFlows` helper mirroring the
  Process endpoint checks at `process.go:220-239`) — and the FR-4 shared
  clone/wiring helper. The BPMN name (`ElementsContainer`, §8.x) is the
  type name. `Process` migration onto the core is **out of scope** (a
  recorded backlog item — no churn in this change-set).
- **FR-2 — `activities.SubProcess`** (new
  `pkg/model/activities/subprocess.go`): embeds the `activity` base
  (boundary events, default flow, properties, roles — `activity.go:20-32`)
  **and** `flow.ElementsContainer`. `ActivityType()` returns
  `flow.SubProcessActivity`. Constructor `NewSubProcess(name string, opts
  ...options.Option)`; inner elements are added via the container's `Add`
  (the process-building idiom unchanged: build nodes, `sp.Add(...)`,
  `flow.Link(...)` — the same-container rule confines inner flows).
- **FR-3 — Shape validation** (`SubProcess.Validate()`, invoked by the
  existing per-node hook in `Process.Validate` and recursively by an
  enclosing SubProcess): realizes ADR-023 §2.3 —
  - flow-endpoint membership for the inner graph (FR-1's helper);
  - exactly one **None** Start Event **XOR** no start event with ≥1
    flow-less activity/gateway; a triggered start, a mixed shape, multiple
    starts, or an empty container → classified errors naming the
    sub-process and the offending node;
  - inner per-node `Validate()` hooks run (recursion: a nested SubProcess
    validates its own body);
  - boundary events of INNER activities must be inner nodes; the
    completeness rule ("an EndEvent requires a StartEvent",
    `snapshot.go:137-145`) applies **per container**.
- **FR-4 — `SubProcess.Clone()`**: deep-clones the inner graph — every
  inner node via its `Clone()` (recursion for nested sub-processes), inner
  flows relinked between the clones, inner gateway default flows remapped,
  inner boundary events rebound to their cloned hosts — the
  `wireClonedGraph` triad (`snapshot.go:285-371`) reproduced for the
  container's own graph via a shared exported helper (`flow` package,
  beside FR-1) that snapshot's wiring delegates to for the top level too
  (one wiring implementation, two callers). The activity base's config
  cloning stays as today (`activity.go:105-128`); the host's own boundary
  events stay cleared for the snapshot rebind.

### Functional — snapshot layer

- **FR-5 — Recursive sweeps.** `snapshot.New` treats a SubProcess node as
  one node of the parent graph (cloned via FR-4 like any node), and the
  static precomputes go **deep**: `hasConditionals` and
  `discoverInstantiatingStarts` walk into containers (a new
  `walkNodesDeep` iterator over `flow.Container` nodes);
  `HasConditionals` is true for a conditional catch/boundary/EBG-arm at
  ANY depth. Instantiating starts remain **top-level only** (an inner
  start is never an instantiation point — ADR-023 §2.3: the parent's token
  is the trigger); the completeness check stays per container (FR-3).
- **FR-6 — Clone parity.** `Snapshot.Clone` needs no new code for nesting
  (FR-4 makes the SubProcess node self-cloning), but the parity is pinned
  by tests: a cloned instance's inner graph is disjoint from the
  definition's and from sibling instances' (the FIX-016/SRD-048
  Clone-drop bug class).

### Functional — runtime layer

- **FR-7 — Tracks carry a scope path.** `track` gains `scopePath
  scope.DataPath` (construction-immutable, the `condDefs` precedent):
  root tracks get the instance root; tracks seeded inside a sub-process
  get the child path. Frames open **at the track's scope path** —
  `instanceScope.openFrame` gains the path parameter
  (`internal/instance/scope.go:80-84`; `scope.NewFrame` already accepts
  `at`) — so data resolution walks child → parent per §10.5.7, and a
  frame commit lands in the child scope. The loop-side conditional
  evaluation (`evalCondition`) opens its transient frame at the OWNING
  track's scope path.
- **FR-8 — The composite parks the host and opens the scope.** The track
  classifies a SubProcess node as a wait node (the `checkNodeType`
  taxonomy): it parks on `evtCh` (`TrackWaitForEvent`) and emits a new
  `trackEvent` kind **`evScopeOpen`** (host track + node). The loop:
  1. derives the child path — the host's scope path + the node's
     **ID-derived segment** (stable across clones; names are not unique);
  2. `plane.OpenScope(child)` — a failure faults the instance (the
     `armBoundaries` failure idiom);
  3. registers a `scopeEntry` in the loop-owned registry (FR-9);
  4. **seeds** the inner tracks per the validated shape — the none-start
     node, or every flow-less inner activity/gateway (the `createTracks`
     filter, `instance.go:265-319`, applied to the container's nodes) —
     each spawned with the child scope path via the existing fork-spawn
     machinery (no loop-goroutine emits: the SRD-048 `atConstruction`
     rule);
  5. reports the scope fact (FR-13).
  A SubProcess with an armed boundary set arms exactly as today — the
  host track occupies the composite node, so `armBoundaries` sees it via
  `boundaryHoster` unchanged.
- **FR-9 — Loop scope registry & drain accounting.** `loopState` gains
  `scopes map[scope.DataPath]*scopeEntry` — `{host *track, node
  flow.Node, parent scope.DataPath, active int}`. Spawn increments the
  track's OWN scope's counter; a terminal event (`evEnded`/`evFailed`/
  `evAwaiting`) decrements it. When a scope's counter reaches zero
  (§13.3.4 drain — awaiting/parked tracks count as active, so "no tokens
  remain" is exact):
  1. `plane.CloseScope(child)` (child data disposed — §10.5.7);
  2. the entry is removed, the scope fact reported;
  3. the **host resumes**: the loop delivers a synthetic completion to the
     host's `evtCh` (the job/task completion idiom, `jobs.go`/`tasks.go`)
     — the host continues to its outgoing selection (conditional/default
     flows per the activity rules).
  The instance's own completion is untouched: `active == 0` on the loop
  remains the global exit; the root needs no `scopeEntry`.
- **FR-10 — Scope-cancel.** A new loop helper `cancelScope(path)`: stop +
  cancel every live track whose `scopePath` has `path` as a prefix
  (cooperative cancellation — `t.stop()`, `t.cancel()`, close of parked
  channels per the `stopAll` idiom, `loop.go:182-205` — scoped instead of
  global), close the affected scopes (children first), drop their
  entries, and clear the scoped entries from the loop registries
  (waiting/msgIdx/conds/watchers of inner tracks). Consumers:
  - **an interrupting boundary on the composite** (`fireBoundary`,
    `boundary_watch.go:167-195`): before cancelling the host track,
    cancel its opened scope — then the existing host-cancel + exception
    flow continuation runs unchanged;
  - **the scoped Terminate** (FR-11);
  - **the error chain** (FR-12).
- **FR-11 — Scoped Terminate.** `execEnv` carries the executing track's
  scope path; `Terminate()` (the `renv` surface, `execenv.go`) routes it:
  the **root** path keeps today's semantics (`evTerminate` → `stopAll`,
  `end.go:141-151` unchanged in behavior); a **nested** path emits a new
  kind **`evScopeTerminate`** — the loop cancels that scope (FR-10),
  closes it, and resumes the host through the normal drain path (the
  composite completes; the parent continues — §13.5.6). Terminate still
  wins over co-located triggers and runs no other end behavior.
- **FR-12 — Error scope-chain.** `applyFailed` (`loop.go:503-512`)
  extends: after the failing activity's own boundary miss
  (`matchErrorBoundary` unchanged), **walk the scope chain** — for each
  enclosing `scopeEntry` (by the failing track's scope path, innermost
  first): match an Error boundary on that entry's composite node
  (`errorRef` code match, the `matchErrorBoundary` matching reused); a
  match cancels that scope (FR-10) and routes the boundary's exception
  flow in the host's parent; no match up to the root → `failFromTrack`
  (the instance fault, unchanged). An **Error End Event** inside a
  sub-process stops faulting the instance directly: its `BpmnError`
  enters the same walk starting at the enclosing composite (the
  end-in-error case narrows to the root scope, ADR-023 §2.6).
- **FR-13 — Observability.** New kind `KindScope` ("scope lifecycle") with
  phases `Opened`/`Completed`/`Terminated`/`Canceled`, `NodeID`/`NodeName`
  = the composite, `Details[scope_path]` (+ the ADR-023 §6
  recommendation). `NodeProgress` on the composite host and the inner
  nodes' facts flow as today.

### Functional — front door

- **FR-14** — runnable example `examples/embedded-subprocess/` (split by
  concern per the >80-line rule; binary gitignored; smoke-run exits 0);
  guide `docs/guides/` gains a composition page (or the section slots
  into a new `composition.md` — decided at M-final with the README);
  `CHANGELOG.md [Unreleased]`; `docs/design/conformance-status.md`: the
  `SubProcess (embedded)` row moves to §1 (same-PR tracker rule) — the
  epic #85 row stays open for the Call Activity slice; `README.md` +
  `README.ru.md` + `examples/README.md` sync.

### Non-functional

- **NFR-1 — Flat processes pay nothing.** No SubProcess in the model → no
  scope entries, no `evScopeOpen`/`evScopeTerminate` traffic, frames open
  at the root as today; the only constant cost is the `scopePath` field
  equality checks on terminal accounting.
- **NFR-2 — Single-writer preserved.** All scope state (`scopes`, drain
  counters) is `loopState`-owned, mutated on the loop goroutine only;
  scope open/close of the data plane happens from the loop (the plane's
  own mutex serializes against executing tracks' commits).
- **NFR-3 — The wiring is written once.** The graph-clone/rewire helper
  (FR-4) is shared between the snapshot top level and the container —
  divergence between the two is the bug class to prevent.
- **NFR-4 — Standard fidelity.** §13.3.4 shapes and drain, §10.5.7
  visibility/disposal, §13.5.6 scoped Terminate, §10.5.1 innermost-catcher
  — each realized as specified; every deviation is already registered in
  ADR-023 (the boundary-crossing rejection).
- **NFR-5 — Coverage.** Touched files 100% (min 80%); diff-coverage gate
  ≥95%; `make ci` green per milestone.

## §3 Models

### §3.1 `flow.ElementsContainer` (new)

```go
// ElementsContainer is the embeddable flow.Container implementation — the
// graph-holding core a Process-like owner composes: id-keyed nodes and
// sequence flows, Add/Remove dispatch by element type, and the
// flow-endpoint validation shared by every container level (ADR-023 §2.2).
type ElementsContainer struct {
	nodes map[string]Node
	flows map[string]*SequenceFlow
}
```

The core carries **no element identity** — the embedding owner (a
Process, a SubProcess) provides `foundation.BaseObject`, avoiding
ambiguous identity selectors when `SubProcess` embeds both the activity
base and the core; together the owner and the core satisfy
`flow.Container`. `Add` delegates as `AddElement(owner, e)` so elements
bind to the OWNER and the same-container rule confines nested graphs.

`Add`/`Remove`/`Elements` mirror `Process.Add` (`process.go:175-209`,
`308-330`); `ValidateFlows()` mirrors the endpoint loop
(`process.go:220-239`). A `CloneInto` helper reproduces the
clone-relink-remap-rebind wiring (`wireClonedGraph`) for any container
graph (FR-4/NFR-3).

### §3.2 `activities.SubProcess` (new)

```go
// SubProcess is the embedded Sub-Process (ADR-023 §2.2): an activity in
// its parent's graph AND the container of its own inner graph. Its inner
// flow is seeded when the host token arrives (the §2.3 validated shapes);
// it completes when its scope drains (§13.3.4).
type SubProcess struct {
	flow.ElementsContainer
	activity // boundary events, default flow, properties, roles
}
```

`SubProcess` lives in `pkg/model/activities` beside the unexported
`activity` base, so the embed is legal (same-package); it composes the
container from `flow` with the activity surface the boundary machinery and
outgoing-selection already consume.

`Validate()` — FR-3 (the shape table). `Clone()` — FR-4. `Exec` is never
reached: the track parks the composite before node execution (FR-8), the
same pattern as the UserTask/worker parks.

### §3.3 Runtime deltas

- `track`: `scopePath scope.DataPath` (construction-immutable).
- `trackEvent`: kinds `evScopeOpen` (host parks on a composite; ev.node =
  the SubProcess) and `evScopeTerminate` (ev carries the scope path);
  `+trackEventKindNames` rows.
- `loopState`: `scopes map[scope.DataPath]*scopeEntry`;

```go
// scopeEntry is one open nested scope, loop-owned (ADR-023 §2.4): the
// parked host resumes when active drains to zero; cancel stops the
// subtree (FR-10).
type scopeEntry struct {
	host   *track
	node   flow.Node
	parent scope.DataPath
	active int
}
```

- `instanceScope.openFrame(trackID, nodeID string, at scope.DataPath)` —
  the path threaded from the track (root for today's callers).
- `execEnv`: carries the track's scope path for the scoped `Terminate`.
- `observability`: `KindScope` + `PhaseOpened` (a NEW phase constant —
  distinct from the engine-lifecycle `PhaseStarted`; the scope's other
  phases reuse the existing `Completed`/`Terminated`/`Canceled`
  vocabulary) + `AttrScopePath`.

## §4 Analysis

### §4.1 Why the host parks on `evtCh` (not `parkCh`, not a new channel)

The composite is a wait-for-completion node — the same shape as a UserTask
(a human completes it) and a worker ServiceTask (a report completes it),
both of which park on `evtCh` and resume on a synthetic completion the
loop sends (`tasks.go:223`, `jobs.go:82`). The scope drain is one more
completion source; reusing the park keeps the deferred-choice flip,
stop-teardown (`stopAll` closes `evtCh`), and the §3.7 discard checkpoint
working for composites with zero new synchronization. `parkCh` is the
join-suspension channel with different fate semantics (survivor/merged) —
wrong contract.

### §4.2 Why seeding reuses the fork-spawn path

Inner tracks are ordinary tracks: spawning them through `spawnForks`-style
loop-side construction (with `atConstruction` classification — the SRD-048
deadlock rule) gives boundary arming, born-parked recording, position
seeding and per-scope accounting in one place. The alternative — the host
track spawning inner goroutines itself — would put track construction on
two goroutines and break the loop's single-writer ownership of the
registries.

### §4.3 Scope identity: ID-derived segments

The child path segment derives from the composite node's **ID** (stable,
unique, clone-independent), not its name (`DataPath` segments must be
unique under one parent; names aren't). The pretty name travels in the
scope fact's details for operators. Nested instances of the same
definition on parallel branches (two tokens entering the same composite —
the implicit-exclusive-merge case, §13.3.1) are serialized per §4.4.

### §4.4 Re-entry: one open scope per composite node

If a second token reaches a composite whose scope is open (a parallel
sibling arrived), the second host **queues**: the loop parks it as waiting
and defers its `evScopeOpen` until the first scope closes (the entry
records the queue). Rationale: one `DataPath` cannot hold two concurrent
scopes; the standard routes concurrent re-entry through multi-instance
(#88 — each MI instance gets its own scope, keyed additionally by the
instance index, per ADR-023 §2.8). Sequential re-entry (the common
loop-back case) reopens a fresh scope — data does not survive across
activations (DataObject lifecycle is per activation, §10.5.7).

### §4.5 Drain correctness

The per-scope counter counts **live tracks** (spawned minus terminal),
identical to the global `active` discipline — parked and awaiting tracks
keep the scope open ("no tokens remain … no nested Activity still
active", §13.3.4). Because all mutation happens in `apply` on the loop
goroutine, drain detection is a zero-race comparison at the decrement
sites; no polling, no second accounting authority.

### §4.6 Rejected shapes

- **A goroutine per scope** (a mini-loop per sub-process): re-introduces
  cross-goroutine lifecycle state for completion/cancel — the exact class
  ADR-017 removed. Rejected.
- **Flattening at snapshot build** (inline the inner graph, prefix ids):
  loses drain semantics, scoped cancel/terminate, per-scope data disposal
  and the error-chain match points (ADR-023 §4). Rejected.
- **Name-derived scope segments**: collide for duplicate names; IDs are
  the invariant. Rejected (§4.3).

### §4.7 Unbounded recursion

ADR-023 §2.9 keeps nesting depth unbounded by design. This SRD enables it
implicitly — `SubProcess.Clone()` (FR-4) and `walkNodesDeep` (FR-5) recurse
naturally, and the loop's scope registry nests by path with no depth
assumption. No static cycle check exists (none is possible for the embedded
form — containment is acyclic by construction: a container physically holds
its children). The operational depth guard of ADR-023 §6 targets the Call
Activity slice (where reference-recursion lives) and is out of this slice's
scope.

## §5 API surface

Public: `flow.ElementsContainer` (+ the shared clone/wiring helper),
`activities.NewSubProcess`/`SubProcess` (Container + ActivityNode +
boundaryHoster), `observability.KindScope`/`AttrScopePath`. Everything
else is `internal/instance` machinery. `Process` is untouched (its
migration onto `ElementsContainer` is backlog).

## §6 Test scenarios

Model (M1–M2): `TestSubProcessShapes` (the FR-3 table: one-none-start ok;
flow-less-seeding ok; triggered/mixed/multiple/empty rejected; nested
recursion validates), `TestSubProcessContainment` (inner flows confined;
cross-boundary link rejected by the same-container rule),
`TestSubProcessClone` (disjoint inner graphs; inner defaults + inner
boundary rebinds pinned — the Clone-drop class),
`TestElementsContainerAddRemove`.

Snapshot (M3): `TestSnapshotNested` (a nested definition snapshots +
clones; inner nodes disjoint per instance), `TestHasConditionalsDeep` (a
conditional catch inside a sub-process flips the flag),
`TestInstantiatingStartsTopLevelOnly`, per-container completeness.

Runtime (M4–M5, `internal/instance`): `TestScopeOpenSeedsNoneStart` and
`TestScopeOpenSeedsFlowlessNodes` (each shape seeds; host parks; scope
fact),
`TestScopeDrainResumesHost` (inner completion → close → host resumes to
outgoing), `TestScopeDataVisibility` (inner frame reads parent data via
walk-up; inner commits land in the child scope and are disposed at
close), `TestScopeReEntryQueues` (§4.4), `TestNestedScopes` (two levels
drain inside-out), `TestBoundaryOnCompositeInterrupts` (inner tracks
cancelled; exception flow runs), `TestScopedTerminate` (inner Terminate
kills only its scope; parent continues; root Terminate unchanged),
`TestErrorScopeChain` (inner failure → composite's Error boundary catches;
no match → instance fault; Error End inside → caught by enclosing
composite), `TestConditionalInsideScope` (a conditional catch in a
sub-process evaluates at its scope path).

E2E (M6, `pkg/thresher`): `TestEmbeddedSubProcessE2E` — a process with a
nested two-level sub-process, data written inside visible to the parent
only through committed outputs, a boundary timeout on the composite, and
the drain-completion path; plus the example smoke.

## §7 Milestones

| # | Scope |
|---|---|
| M1 | `flow.ElementsContainer` + `SubProcess` model + shape validation (FR-1/2/3) + tests |
| M2 | `SubProcess.Clone` + the shared wiring helper (FR-4, NFR-3) + clone-parity tests |
| M3 | Snapshot recursion: deep sweeps + parity pins (FR-5/6) + tests |
| M4 | Runtime core: `scopePath`, `evScopeOpen`, the scope registry, open/seed/park/drain/close/resume, frames-at-path, scoped conditional eval (FR-7/8/9, part of FR-13) + tests |
| M5 | Scope-cancel: boundary-on-composite, `evScopeTerminate`, the error scope-chain (FR-10/11/12) + tests |
| M6 | Thresher e2e + example + changelog + tracker row + README/guide sync (FR-14) |

Post-M6: `/check-srd`, §10 fill, status flip (ADR-023 stays Draft until
the Call Activity slice lands — the workstream decision), linked-docs
sync, PR handover.

## §8 Cross-doc

- Implements [ADR-023 v.1](../design/ADR-023-sub-process-and-call-activity.md)
  §2.1–§2.6/§2.9 (traced per FR above).
- Extends [ADR-001 v.6](../design/ADR-001-execution-model.md) accounting;
  applies [ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md)
  to composite hosts; realizes
  [ADR-006 v.3](../design/ADR-006-events-and-subscriptions.md) §2.6;
  activates [ADR-010 v.2](../design/ADR-010-process-data-model.md) §2.2
  child scopes; extends [ADR-013 v.2](../design/ADR-013-observability.md)
  with `KindScope`.

## §9 Definition of Done

- [ ] All FR/NFR wired and traced to §6 tests.
- [ ] `make ci` green per milestone (lint 0, race, diff-coverage ≥95%,
      vuln 0); touched files 100% (min 80%).
- [ ] Example runs to completion (exit 0), binary gitignored.
- [ ] Conformance tracker: the `SubProcess (embedded)` row flips in this
      PR; #85 remains open for the Call Activity slice.
- [ ] Changelog `[Unreleased]` before the PR description.
- [ ] `/check-srd` PASS; §10 filled; linked docs synced.

## §10 Implementation summary

### §10.1 Milestones by commit (branch `feat/sub-processes`)

| Stage | Commit | Scope |
|---|---|---|
| doc | `29f4b28` | SRD-049 (after ADR-023 `dc02515` and the extract appendix) |
| M1 | `a2d88f8` | `flow/container.go` (interface move + `ElementsContainer`) + `SubProcess` + shape validation |
| M2 | `e7d63c9` | `WireClonedGraph` moved to flow (snapshot delegates) + `CloneGraph` + `SubProcess.Clone` |
| M3 | `895ec13` | `walkNodesDeep` + deep `hasConditionals`; instantiating starts pinned top-level |
| M4 | `38e93f5` | `scopePath`, frames-at-path, `evScopeOpen`, the scope registry, open/seed/park/drain/close/resume, `KindScope` |
| M5+fix | `d2f2c9c` | `cancelScope`, `evScopeTerminate`, the error scope-chain — plus the folded conditional lost-wake-up FIX (below) |
| M6 | `d10482b` | thresher e2e, `examples/embedded-subprocess/`, `docs/guides/composition.md`, changelog, tracker, READMEs |

Every milestone landed `make ci` green; the branch-final diff-coverage:
96.9% of 715 changed lines (min 95%).

### §10.2 Deltas vs the draft

- **§3.2 said "Exec is never reached" — wrong.** The host resumes via the
  synthetic completion, and the composite node then executes normally:
  `SubProcess.Exec` runs the standard outgoing selection, and
  `ProcessEvent` accepts the completion delivery (the UserTask post-
  completion shape). Both landed in M4.
- **`SubProcess.Node()` override added** (the ServiceTask convention):
  without it, flow targets unwrap to the embedded activity base —
  stripping the executor/container capabilities. Found by the first
  failing M4 run; the concrete-type override is now part of the
  composite's required surface.
- **FR-1 landed as the concern file** (owner direction during review):
  `container.go` holds the moved `Container` interface, the core, and the
  shared wiring helper together; the §3.1 identity-embed correction
  (the core carries no `BaseElement`) landed with M1.
- **`clearConds` keyed by trackID** (the disarm sites carry the id), and
  `apply`'s wait/deliver plane extracted into `applyWaitPlane` (gocyclo).
- **Fixes found on sight** (M4): `unregisterEvent` skips Conditional
  definitions (never hub-registered — SRD-048 symmetry); `KindScope`
  joined the observability echo-level table (an absent kind echoes at
  Error); `executeNode`'s NodeExecutor error names the node.
- §6's `TestScopeDrainResumesHost` landed folded into
  `TestScopeOpenSeedsNoneStart`/`...FlowlessNodes` (the drain-resume
  assert is their outer-task check).

### §10.3 Empirical findings

- **The conditional lost-wake-up flake (fixed in M5, pre-existing on
  master from the SRD-048 merge).** A track walking onto a conditional
  catch as its continuation node armed the subscription (`evWaiting`) and
  then its `evMoved`-driven boundary disarm tore the fresh watch down
  (`disarmBoundaries`' unconditional `clearConds`); the later commit
  swept an empty registry — a silently lost wake-up, the instance hung
  Active (~1 in 4-8 under race; fork-born catches unaffected, making the
  per-instance clone's flow-map iteration order the flake selector).
  Root-caused from a recorded fact/log trail (the Registered fact's stale
  node proved arm-before-move). Fix: the disarm is boundary-flavor-scoped
  (`clearCondBoundaries`); the arm also uses the wait node carried on the
  emit for correct fact attribution. Pinned deterministically by
  `TestConditionalSurvivesMove`; the e2e passes ×25 under race.
- **The M1 `make ci` red was a process error, not a code error**: the
  gate ran against a tree still being edited (the recorded concurrent-ci
  pitfall) — re-run clean on the frozen commit.

### §10.4 Backlog (out of SRD-049 scope)

- **`Process` migration onto `flow.ElementsContainer`** — the recorded
  FR-1 deferral (no churn in this change-set).
- The Call Activity slice (ADR-023 §2.7) — the next SRD; #85 remains
  open for it.
- The fork-born Message-catch synchronous-fire corner (SRD-048 §10.4)
  remains queued.

## Open questions

None.
