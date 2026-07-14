# SRD-044 — Structural data S3: commit-diff change detection + DataChange facts

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-07-14 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-011 v.6](../design/ADR-011-process-data-flow.md) §2.9.4 (change notification by commit-diff — the changed-path seam and its DataChange consumer) — the S3 slice (§2.9.6). ADR-011 v.6 is already Accepted; S3 is a slice of it and does **not** bump the ADR. |
| Upstream | [ADR-013 v.2](../design/ADR-013-instance-observability.md) §2.6/§2.7 (the `KindDataChange` fact vocabulary this wires — landed but deferred), [ADR-010 v.2](../design/ADR-010-process-data-model.md) §2.7/§10.4.2 (the data plane / commit boundary), [ADR-012 v.1](../design/ADR-012-execution-layering.md) (public contracts in `pkg/*`) |
| Refines | SRD-042 (S1 read path), SRD-043 (S2 write path) — by number, sideways |

S1 (SRD-042) made a value navigable by path; S2 (SRD-043) made it writable and
assemblable by path. S3 answers **"which data changed"** — computed at the
commit boundary by **diffing** the committed value graph, produced as a
`(path, ChangeType)` set, and consumed by its first consumer: the **DataChange**
observability facts ADR-013 v.2 reserved `KindDataChange` for and deferred to
this data-plane rework. It is the 13th and last catalog kind SRD-041 left
unwired.

The decision is explicitly **against an in-value callback** (ADR-011 v.6 §2.9.4,
reopening the §2.7 maintenance rule and deciding it): a value type still embeds
no notification; the **scope** detects change at `Commit`. Conditional Events —
the *other* future consumer of the same seam — stay element-completion work
(ADR-006 v.2); S3 builds the seam and the DataChange consumer only.

## 1. Background

- **The seam is designed but empty.** ADR-011 v.6 §2.9.4 prescribes commit-diff:
  "At `Scope.Commit` the scope **diffs** the committed value graph against its
  prior and produces a **committed changed-path set**, exposed as an internal
  seam." No diff code exists yet — `Scope.Commit` (`internal/scope/scope.go:220`)
  is a blind whole-datum replace (`vv[names[i]] = d`).
- **The vocabulary is landed but unwired.** `data.ChangeType`
  (`Value_Added/Updated/Deleted`, `pkg/model/data/value.go:121`) has **zero
  usages**; `observability.KindDataChange` + `PhaseValue{Added,Updated,Deleted}`
  (`pkg/observability/fact.go:29,97`) have **zero emitters**. The completeness
  canary records the gap: *"KindDataChange is the sole ⏳-deferred 13th kind
  (ADR-011 data-plane, no data-element change facts yet)"*
  (`pkg/thresher/completeness_test.go:8`).
- **The `Updater`/`UpdateCallback` machinery was already deleted in S1** (SRD-042)
  as the stale in-value mechanism §2.9.4 supersedes; `ChangeType` was kept and
  retargeted for exactly this slice.

## 2. Requirements

### Functional

- **FR-1 — the diff engine.** A new `data.Change{Path, Type}` and a pure function
  `data.DiffValues(root string, old, new data.Value) []data.Change` that
  recursively compares two value graphs and returns one entry per changed path
  (§3.1). Rooted at `root` (the committed name); nested paths use the S1 grammar
  (`root.field`, `root.items[0].price`).
- **FR-2 — commit computes the diff.** `Scope.Commit` returns
  `([]data.Change, error)`: for each committed name it diffs the **prior**
  committed datum (`vv[name]`, if any) against the incoming one under the
  existing commit lock, aggregating the per-name changes (§3.2). No behavior
  change to the whole-datum replace itself.
- **FR-3 — the diff reaches the emitting layer.** `Frame.Commit` returns
  `([]data.Change, error)`, propagating the scope's changed-path set to its track
  caller. The instance-birth commit sites (`instanceScope.load`,
  `instanceScope.bindEventPayload`) call `Commit` and **ignore** the changes —
  initial state is not a change (§4.4).
- **FR-4 — DataChange facts.** At the `Frame.Commit` site the track emits **one**
  `observability.Fact` per change: `Kind = KindDataChange`,
  `Phase = phaseFor(change.Type)`, `NodeID`/`NodeName` = the committing node,
  `Details[AttrDataPath] = change.Path`, via `Instance.report` — the single
  emission point (Scope holds no Reporter, §4.2).
- **FR-5 — the vocabulary bridge.** Add `observability.AttrDataPath` and a
  package-level `data.ChangeType → observability.Phase` lookup map at the
  emitting layer (a table, not a switch).
- **FR-6 — worked example + canary.** A runnable example registers an observer
  and shows DataChange facts as a node commits a structured output; the
  completeness canary (`TestEngineScopeEmissionCompleteness`) is updated so
  `KindDataChange` is now **asserted emitted**, not excluded as deferred.

### Non-functional

- **NFR-1 — no in-value notification.** The `data.Value` interface gains nothing;
  change is detected by the scope at the commit boundary. §2.7's
  value-vs-notification separation stays intact (ADR-011 v.6 §2.9.4).
- **NFR-2 — bounded, listener-guarded cost.** The diff is `O(committed value
  size)`, computed under the existing commit lock, allocating no change slice
  when nothing changed. Emission keeps `Instance.report`'s hot-path guard (no
  listeners → no fact built). Gating the *diff computation itself* on observer
  presence is a noted future optimization, not S3.
- **NFR-3 — no operator-log flood.** `KindDataChange` stays **no-echo**
  (`kindNoEcho`, already wired) — DataChange fans to observers only, never the
  operator log.

## 3. Models

### 3.1 The diff (`pkg/model/data/`)

```go
// Change is one entry of a commit-diff: a data path and how it changed
// (ADR-011 v.6 §2.9.4). Path is rooted at the committed name and uses the
// structural grammar ("order", "order.items[0].price").
type Change struct {
    Path string
    Type ChangeType
}

// DiffValues compares old and new value graphs rooted at `root` and returns one
// Change per changed path. A nil old (or new) means the whole (sub)value was
// added (or deleted). Returns nil when nothing changed.
func DiffValues(root string, old, new Value) []Change
```

Recursion rules (positional; identity-tracking is out of scope):

| old | new | Rule |
|---|---|---|
| nil | present | one **Value_Added** at `path` (the whole subtree is new — no descent) |
| present | nil | one **Value_Deleted** at `path` (no descent) |
| scalar | scalar | `Get()` differs → one **Value_Updated** at `path`; equal → nothing |
| Record | Record | recurse over `union(old.Keys, new.Keys)` at `path.key` |
| Collection | Collection | recurse over `[0, max(oldLen, newLen))` at `path[i]` |
| kind A | kind B (A≠B) | one **Value_Updated** at `path` (shape changed — no descent) |

A **newly-added or removed subtree yields one change at its root**, not one per
leaf; only *within-shape* differences descend. Scalar equality is by `Get()`
value comparison (`reflect.DeepEqual` on the boxed `any`). A `Collection` element
that is a **raw scalar** (not itself a `data.Value` — e.g. an `Array[string]`
element) is compared by value like a scalar leaf, mirroring S1's `scalarLeaf`
handling in `WalkSteps`; an element that *is* a `data.Value` recurses. The
`ChangeType` values are the ones observability mirrors: `data.ValueAdded` ↔
`observability.PhaseValueAdded`, and likewise for Updated/Deleted (§3.3).

### 3.1a Worked example

Node `reprice` re-commits `order` from
`{total:100, items:[{price:50},{price:100}]}` to
`{total:150, items:[{price:50},{price:100},{price:20}]}`.
`DiffValues("order", old, new)` returns:

| Path | ChangeType | Why |
|---|---|---|
| `order.total` | `Value_Updated` | scalar `100`→`150` |
| `order.items[2]` | `Value_Added` | new element — **one** change at its root (its `.price` is *not* exploded) |

`order.items[0]` / `[1]` are unchanged → no entry. The track then emits two
facts: `{Kind: DataChange, Phase: Value_Updated, NodeID: reprice, Details:
{data_path: "order.total"}}` and `{…, Phase: Value_Added, Details: {data_path:
"order.items[2]"}}`.

### 3.2 Commit returns the diff (`internal/scope/`)

```go
// before: func (p *Scope) Commit(at DataPath, dd ...data.Data) error
func (p *Scope) Commit(at DataPath, dd ...data.Data) ([]data.Change, error)

// before: func (f *Frame) Commit() error
func (f *Frame) Commit() ([]data.Change, error)
```

`Scope.Commit` captures `prior := vv[name]` before `vv[name] = d`, calls
`data.DiffValues(name, priorValue, newValue)` per committed name (a nil prior →
all-added), and returns the concatenation. The mutation and locking are
unchanged.

### 3.3 Emission (`internal/instance/`, `pkg/observability/`)

```go
// pkg/observability/fact.go
AttrDataPath = "data_path"

// internal/instance — the ChangeType→Phase table (data-declaration, not switch)
var dataChangePhase = map[data.ChangeType]observability.Phase{
    data.ValueAdded:   observability.PhaseValueAdded,
    data.ValueUpdated: observability.PhaseValueUpdated,
    data.ValueDeleted: observability.PhaseValueDeleted,
}
```

At the `Frame.Commit` site (track), per returned `Change`:

```go
t.instance.report(observability.Fact{
    Kind:     observability.KindDataChange,
    Phase:    dataChangePhase[c.Type],
    NodeID:   step.node.ID(),
    NodeName: step.node.Name(),
    Details:  map[string]string{observability.AttrDataPath: c.Path},
})
```

## 4. Analysis & decisions

### 4.1 Diff at the scope, returned as pure data

The scope is the only layer holding both the prior committed value (`vv[name]`)
and the incoming one, so it is the only place the diff is authoritative
(ADR-011 v.6 §2.9.4: "the scope owns commit, so the diff is authoritative").
`DiffValues` returns `[]data.Change` — pure data, no observability import — so
the data plane never names its consumers (§2.9.4). *Alternatives:* an in-value
subscription (rejected by the ADR — fragile under frame-clone-then-replace,
which drops callbacks on clone); computing the diff in the frame (rejected — the
frame does not hold the prior committed value for a name it never read).

### 4.2 Emission at the instance, not the scope

`Scope` holds no `Reporter`; `Instance.report` (`internal/instance/observer.go`)
is the single emission point, reaching the sink via `EngineRuntime.Reporter()`.
So the scope produces the change set and the **track** (which has both the
committing node and `t.instance`) emits. This preserves the ADR-013 §2.7
single-producer contract and keeps `internal/scope` free of an observability
dependency.

### 4.3 Recursive per-path granularity

Confirmed with the owner: descend into records/lists so a deep scalar edit is a
`Value_Updated` at its leaf path and an appended element is a `Value_Added` at
`[i]` — the literal `(path, ChangeType)` the ADR describes and the precise
substrate conditional events will later evaluate against. A brand-new or removed
subtree is **one** change at its root (not exploded per leaf), keeping first-set
of a structured variable to a single `Value_Added`. *Alternative:* variable-level
only (rejected — loses the structural precision §2.9.4 calls for).

### 4.4 Activity-boundary commits only

Confirmed with the owner: emit only at `Frame.Commit` — a running node's output
commit, the ADR's correct visibility boundary (§10.4.2 wires output copies into
Completing→Completed). The instance-birth commits (`load` of process properties,
`bindEventPayload`) are **initial state**, not changes during execution, and
carry no node — they call `Commit` and drop the returned changes.

### 4.5 Path-only Details

Confirmed with the owner: `Details` carries the changed path (`AttrDataPath`); the
kind is already in `Phase`. No value is included — that would reopen the
sensitive-data masking rule (ADR-010/011) for no S3 benefit; path + kind is
enough for observability and the conditional-event substrate. A masked value can
be added later without changing the seam.

### 4.6 Commit signature change

`Scope.Commit`/`Frame.Commit` move from `error` to `([]data.Change, error)`. Both
are `internal/*` — no public API changes. Three call sites adapt: the two
instance-birth sites discard the changes; the `Frame.Commit` caller in the track
propagates and emits.

## 5. API / contract

No **public** surface changes beyond `pkg/model/data` and `pkg/observability`
(both already public):

- `pkg/model/data`: **new** `data.Change` struct, **new** `data.DiffValues`
  function. `data.ChangeType` and its constants already exist (retargeted here).
- `pkg/observability`: **new** `AttrDataPath` constant. `KindDataChange` /
  `PhaseValue*` already exist.
- `internal/scope`, `internal/instance`: signature changes are internal.

## 6. Test scenarios

| # | Test | Covers |
|---|---|---|
| T-1 | `TestDiffValues` (`pkg/model/data`) | every §3.1 rule: nil→scalar (Added root), scalar→nil (Deleted), scalar change (Updated leaf) / no-change (empty), record field add/remove/change, collection append/remove/element-change, nested record-in-list, kind-change (scalar↔record → Updated no-descent), new subtree = one Added at root |
| T-2 | `TestCommitReturnsDiff` (`internal/scope`) | committing a name the first time → all-added; re-committing a changed structured value → per-path changes; unchanged re-commit → empty |
| T-3 | `TestFrameCommitPropagatesDiff` (`internal/instance` or scope) | `Frame.Commit` returns the scope's change set |
| T-4 | `TestDataChangeFactsEmitted` (`internal/instance` or `pkg/thresher`) | a node committing a structured output emits one `KindDataChange` fact per changed path (right `Phase`, `NodeID`, `AttrDataPath`) to a registered observer; a birth-init commit emits none |
| T-5 | `TestEngineScopeEmissionCompleteness` (updated) | `KindDataChange` now **asserted emitted** (the 13th kind), the deferral exclusion removed |
| T-6 | example smoke | the observer example prints DataChange facts; exits 0 |

## 7. Milestones

| # | Scope |
|---|---|
| **M1** | FR-1: `data.Change` + `data.DiffValues` (the pure diff engine). T-1. |
| **M2** | FR-2/FR-3: `Scope.Commit` + `Frame.Commit` return the diff; birth-init sites adapt. T-2, T-3. |
| **M3** | FR-4/FR-5: `AttrDataPath` + the phase table + track emission; the completeness canary now asserts DataChange. T-4, T-5. |
| **M4** | FR-6: the worked observer example + smoke (T-6); §10; `/check-srd`; SRD-044 → Accepted; sync linked docs (roadmap + SAD-001 → S3 landed). ADR-011 v.6 stays Accepted — S3 is a slice, no bump. |

## 8. Cross-doc

| Ref | Version | Direction | Role |
|---|---|---|---|
| ADR-011 | v.6 (Accepted) | SRD → ADR (up) | implements §2.9.4 (S3); **no bump** |
| ADR-013 | v.2 | up | the `KindDataChange` / `PhaseValue*` fact vocabulary this wires (landed, deferred) |
| ADR-010 | v.2 | up | the data plane / commit boundary (§10.4.2) the diff runs at |
| ADR-012 | v.1 | up | public contracts in `pkg/*` |
| SRD-042 / SRD-043 | (one-shot, by number) | sideways | the S1 read / S2 write paths this extends |

## 9. Definition of Done

- [x] FR-1..FR-6 wired; every §6 test exists and is green.
- [x] `DiffValues` covers every §3.1 rule; a new/removed subtree is one change at
      its root; unchanged commit → no changes, no allocation.
- [x] DataChange facts emit at activity-boundary commits only; birth-init emits
      none; `KindDataChange` stays no-echo.
- [x] The completeness canary asserts the 13th kind; no kind remains deferred.
- [x] `make ci` green; diff-coverage ≥95% (aim 100%); full `-race`; example smoke
      exits 0.
- [x] SRD-044 flipped to Accepted; roadmap + SAD-001 S3 note. ADR-011 v.6
      unchanged.
- [x] §10 filled with milestone SHAs and deltas.

## 10. Implementation summary

Landed on `feat/structural-commit-diff` in four milestones.

### 10.1 Milestones

| # | Commit | Scope | Tests |
|---|---|---|---|
| doc | `be8b022` | this SRD | — |
| M1 | `0300bfd` | `data.Change` + `data.DiffValues` (the pure diff engine) | `TestDiffValues` |
| M2 | `a12dddf` | `Scope.Commit`/`Frame.Commit` → `([]data.Change, error)`; birth-init sites drop the set | `TestCommitReturnsDiff`, `TestFrameCommitPropagatesDiff` |
| M3 | `cd33d51` | `AttrDataPath`, the `dataChangePhase` table, `track.reportDataChanges`; canary asserts 13 kinds | `TestDataChangeFactsEmitted`, `assertAll13Kinds` |
| M4 | (this) | the `data-change` worked example + README/index wiring; §10; Accepted flip | example smoke (T-6) |

All touched functions at 100% coverage; `make ci` green at each milestone.

### 10.2 Deltas vs the §3 draft

- **No new canary scenario.** §6 T-5 anticipated giving a scenario a
  data-committing bit; the UserTask scenario's completion output (`result`)
  already commits through its track frame, so the 13th kind surfaced with no
  scenario change — only the assertion upgraded (`assertAll13Kinds` + the
  representative `DataChange/Value_Added`).
- **Branchless emission at the track.** `finalizeNodeExecution` reports before
  returning `Frame.Commit`'s error (`changes, err := f.Commit();
  t.reportDataChanges(...); return err`) — a failed commit returns a nil set,
  so the report is naturally a no-op; no untestable error branch.
- **Node attribution is Name-first.** A node's `ID()` is engine-generated
  (numeric); the human-readable attribution in a fact is `NodeName` — T-4
  asserts the name and only non-emptiness of the id.
- **The pre-existing error-only Commit assertions** in the scope package were
  adapted via one `errOf` helper rather than rewritten (every original
  assertion intact).

## Open questions

None. The diff granularity (recursive per-path, new/removed subtree = one change
at its root), the emission scope (activity-boundary commits only), and the fact
payload (path only) are decided above with the owner. The Conditional-Event
consumer of the same seam is explicitly **out of scope** (ADR-006 element work);
a masked value in Details and gating the diff on observer presence are noted
future work, not open questions for S3.
