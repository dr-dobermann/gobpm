# SRD-055 — Multi-Instance (sequential)

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-19 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-025 v.1](../design/ADR-025-activity-iteration-loop-and-multi-instance.md) §2.4–§2.7, §2.9 (the Multi-Instance model; the **sequential** slice — §2.8 `behavior` deferred to SRD-056; epic #88) |
| Upstream | [ADR-011 v.7](../design/ADR-011-process-data-flow.md) (the structural `data.Collection` — `Count`/`GetAt`/`SetAt` — the split/assemble mediator rides), [ADR-010 v.2](../design/ADR-010-process-data-model.md) (name-based data-plane resolution), [ADR-023 v.2](../design/ADR-023-sub-process-and-call-activity.md) (the composite child-scope re-entry seam this reuses), [ADR-013 v.2](../design/ADR-013-instance-observability.md) (iteration facts), [ADR-001 v.6](../design/ADR-001-execution-model.md) (the loop owns node execution) |
| Refines | — |
| Related | SRD-054 (the Standard Loop composite-iteration seam this generalizes) |

## §1 Background

[SRD-054](SRD-054-standard-loop.md) landed **Standard Loop** and, with it, the
reusable **composite iteration** substrate: a looped Sub-Process / Call Activity
**re-opens its child scope per pass** through `resumeScopeHost` /
`onScopeOpen`, with a loop-owned per-host `loopCounter` bound into the scope
(`bindLoopCounterAt`) each pass. ADR-025 §2.4–§2.9 decided **Multi-Instance**;
this SRD lands its **sequential** slice.

Multi-Instance sequential (§13.3.7) runs an activity **N times, one after
another** — N fixed once at activation — typically **once per element of a
collection**. It is structurally the *same* "re-open the scope N times" machine
Standard Loop already drives, differing in the **continuation predicate** (a
fixed count instead of a boolean `loopCondition`) and adding a **per-instance
data mediator** (split a collection in, assemble one out) and a
`completionCondition`. This SRD therefore **generalizes** the SRD-054 seam rather
than duplicating it.

**In scope:** cardinality (both sources), the sequential re-entry, the
split/assemble data mediator with a completion-time visibility barrier,
`completionCondition`, and the MI runtime attributes. **Deferred:**
`behavior`/`ComplexBehaviorDefinition` event-throwing → **SRD-056** (with parallel
MI); MI compensation → the future Transaction work (ADR-025 §2.10).

## §2 Requirements

### Functional — the model

- **FR-1 — `MultiInstanceLoopCharacteristics` type.** A second
  `LoopCharacteristics` implementation (`activities/loop.go` sealed interface),
  in its own file `multiinstance.go`, carrying `isSequential` (bool),
  `loopCardinality` (`data.FormalExpression`, nil ⇒ collection-driven),
  `loopDataInputRef` / `loopDataOutputRef` / `inputDataItem` / `outputDataItem`
  (**string names**, resolved against the scope by name — matching the data
  plane's name-based model, `plane.GetData` / `bindLoopCounterAt`), and
  `completionCondition` (`data.FormalExpression`, nil ⇒ run all N). It embeds
  `foundation.BaseElement`.
- **FR-2 — construction validates all inputs.** `NewMultiInstance(opts…)` (with
  `MultiInstanceOption` closures, mirroring `NewStandardLoop`): **exactly one** of
  `loopCardinality` / `loopDataInputRef` is set (XOR — ADR-025 §2.4 "alternative
  sources, not composable"); `loopCardinality.ResultType() == "int"` when present;
  `completionCondition.ResultType() == "bool"` when present; a collection-driven
  MI requires `inputDataItem` (the per-instance name to bind). Self-identifying
  errors.
- **FR-3 — Event Sub-Process rejects MI (inherited).** A `triggeredByEvent`
  `SubProcess` carrying any `LoopCharacteristics` already fails validation
  (SRD-054 FR-3a, keyed on the shared interface) — MI is covered with no new
  guard; add a test asserting it holds for the MI marker.

### Functional — runtime detection & the shared seam

- **FR-4 — `multiInstanceOf` detector.** A capability detector (new
  `internal/instance/mi.go`, sibling to `std_loop.go`'s `standardLoopOf`) reports
  the MI characteristics off a node's `LoopCharacteristics()`. The two detectors
  are mutually exclusive by construction (distinct capability shapes).
- **FR-5 — one shared composite-iteration seam.** The two Standard-Loop-specific
  blocks in `onScopeOpen` and `resumeScopeHost` (`scope_runtime.go`) are
  refactored into a single **`compositeIterator`** dispatch — Standard Loop and
  MI become two **strategies** over one re-entry mechanism, not two near-duplicate
  blocks. The Standard-Loop strategy stays in `std_loop.go`; the MI strategy lives
  in `mi.go`. SRD-054's tests re-verify the Standard-Loop strategy is behaviour-
  preserving.

### Functional — cardinality & sequential execution

- **FR-6 — cardinality once at activation.** N is resolved **exactly once**, at
  the first `onScopeOpen` for the host (`host.loopCounter == 0`): `loopCardinality`
  evaluated to an `int` (the `evalLoopCond`-shaped transient-frame + `Evaluate`
  path), **or** the referenced collection's `Count()`
  (`plane.GetData(host.scopePath, loopDataInputRef).(data.Collection).Count()`). N
  is frozen for the activity's lifetime (§13.3.7) — later collection growth spawns
  no instances. N ≤ 0 completes the activity with zero instances.
- **FR-7 — sequential re-entry.** Instance *i+1*'s scope opens only after
  instance *i*'s has drained: the count-driven continuation re-opens via the
  existing seam (`resumeScopeHost` → `onScopeOpen`) while `loopCounter+1 < N`, else
  the host resumes and the activity follows its single outgoing flow once. At most
  one instance scope is ever open.

### Functional — the data mediator

- **FR-8 — split in.** Before instance *i* runs, the engine binds `inputDataItem`
  = element *i* of the `loopDataInputRef` collection into the instance's **child
  scope** (a new `bindDataItemAt`, modeled on `bindLoopCounterAt`); the body reads
  it by name, exactly like `loopCounter`.
- **FR-9 — assemble out.** When instance *i* drains, the engine reads its
  `outputDataItem` from the child scope and writes it into slot *i* of a
  **private staging** output collection (a host-held `values.Array`, via
  `Collection.SetAt`) — positional (output slot = input ordinal), deterministic.
- **FR-10 — visibility barrier.** The staging output collection is **not**
  scope-visible during the run (no per-slot commit, so no partial reads and no
  per-slot `DataChange` facts); it is published under `loopDataOutputRef` at the
  host scope **once**, at activity completion (ADR-025 §2.6). A cancelled or
  never-run instance leaves its slot at its pre-run value.

### Functional — completion condition & runtime attributes

- **FR-11 — `completionCondition`.** Evaluated after each instance drains (in the
  MI strategy's continuation, the `evalLoopCond`-shaped path): `true` → the
  activity is **done now** — no further instances launch, the output publishes,
  the host resumes; `false` → the next instance launches (or the activity
  completes when all N are done). For **sequential** MI "cancel remaining" is
  simply "stop launching" — one instance runs at a time, so there is **no**
  active-scope teardown (no `cancelScope`).
- **FR-12 — runtime attributes.** A per-host `miState` (loop-owned, on the track:
  frozen N, `numberOfCompletedInstances`, the staging output) publishes
  `numberOfInstances` / `numberOfCompletedInstances` / `numberOfActiveInstances`
  (0 or 1 for sequential) into the host scope alongside `loopCounter` each pass, so
  `completionCondition` and the body resolve them by name (walk-up). These are
  **not** routed through the instance-global `RuntimeVar` supplier — they are
  per-host-per-scope.

### Functional — observability & front door

- **FR-13 — observability.** Each instance's scope facts carry the iteration
  ordinal (the SRD-054 `AttrLoopCounter` on `reportScope`), so MI passes are
  individually observable.
- **FR-14 — front door.** A runnable `examples/multi-instance-sequential/`, the
  iteration guide (`docs/guides/iteration.md` — add the MI section), `CHANGELOG.md`,
  the conformance tracker row 4, and the READMEs (EN + RU) reflect MI-sequential.

### Non-functional

- **NFR-1 — reuse, don't duplicate.** No new scope-lifecycle / seeding / drain /
  queue / cancel code — MI rides the SRD-054 re-entry seam via the shared
  `compositeIterator`. The one genuinely new runtime code is the MI strategy
  (`mi.go`) + `bindDataItemAt` + the `miState`.
- **NFR-2 — reuse the expression path.** `loopCardinality` and
  `completionCondition` evaluate through the existing `ExpressionEngine().Evaluate`
  + result-assert path (`evalLoopCond`), no new evaluator.
- **NFR-3 — deferred surfaces stay out.** No `behavior` / `ComplexBehaviorDefinition`
  (SRD-056), no compensation (future). No parallel execution.
- **NFR-4 — coverage.** Every touched file finishes ≥95% diff-coverage (aim 100%);
  `make ci` green.

## §3 Models

### §3.1 `pkg/model/activities/multiinstance.go` — the MI type

```go
// MultiInstanceLoopCharacteristics is a Multi-Instance marker (BPMN §13.3.7):
// the activity runs N times (fixed at activation). This slice implements the
// sequential shape (isSequential = true).
type MultiInstanceLoopCharacteristics struct {
    foundation.BaseElement
    loopCardinality     data.FormalExpression // int expr; nil ⇒ collection-driven
    completionCondition data.FormalExpression // bool expr; nil ⇒ run all N
    loopDataInputRef    string                // input collection name in scope
    loopDataOutputRef   string                // output collection name in scope
    inputDataItem       string                // per-instance input datum name
    outputDataItem      string                // per-instance output datum name
    isSequential        bool
}

func (*MultiInstanceLoopCharacteristics) isLoopCharacteristics() {}

func NewMultiInstance(opts ...MultiInstanceOption) (*MultiInstanceLoopCharacteristics, error)
```

Options: `WithSequential()`, `WithCardinality(expr)`, `WithInputCollection(ref,
item string)`, `WithOutputCollection(ref, item string)`, `WithCompletionCondition(expr)`.
Getters expose the fields to the runtime. Unlike `NewStandardLoop`, which takes
its single always-required arg (`loopCondition`) positionally, `NewMultiInstance`
puts *everything* into options — MI has **no** single always-required arg (the
cardinality source is a XOR of two), so both sources are optional at the type
level and the XOR is enforced in the constructor body (FR-2).

### §3.2 Runtime deltas (`internal/instance/`)

- **`compositeIterator`** (interface) + the dispatch in `scope_runtime.go`:
  `firstPass(host)` (bind ordinal / bind input item; report a zero-iteration skip)
  and `nextPass(host) (reopen bool)` (advance, test the terminal condition, bind
  next input / assemble prior output). `standardLoopOf` and `multiInstanceOf`
  return strategies; the `onScopeOpen`/`resumeScopeHost` blocks collapse to one
  `compositeIteratorOf(node)` dispatch.
- **`mi.go`** — `multiInstanceOf` + the MI strategy: cardinality resolution, the
  input split, the output staging + assemble, `completionCondition`, the `miState`.
- **`scope.go`** — `bindDataItemAt(path, name string, value any) error` (the
  `bindLoopCounterAt` sibling for a named per-instance datum).
- **`track.go`** — a loop-owned `miState *miState` field (frozen N, completed
  count, staging `values.Array`).

## §4 Analysis

### §4.1 MI generalizes the SRD-054 seam (NFR-1)

The composite re-entry already re-opens the child scope N times: `resumeScopeHost`
(`scope_runtime.go`) increments the ordinal, tests a terminal condition
(`reachedMax` / `evalLoopCond`), and either re-opens via `onScopeOpen` or resumes
the host. MI is the **same control flow** with (a) the terminal test = a count
comparison + `completionCondition`, (b) an input bind on open, (c) an output
assemble on drain. Extracting a `compositeIterator` strategy makes Standard Loop
and MI two implementations of one seam (owner-chosen: shared seam, MI in its own
file) — DRY, and a future MI-vs-loop divergence can't happen by accident.

### §4.2 Data references are names, not pointers (FR-1, FR-8/9)

The data plane resolves everything by name (`plane.GetData(from, name)`,
`frame.GetData(name)`; `loopCounter` is bound and read as the bare name
`"loopCounter"`). Modeling `loopDataInputRef` / `inputDataItem` / etc. as **string
names** keeps MI consistent with that model: the input collection is any scoped
datum whose value is a `data.Collection`; the mediator reads element *i* with
`GetAt` and binds `inputDataItem` with the `bindLoopCounterAt` mechanism. Object
pointers would fight the walk-up resolution the whole engine uses.

### §4.3 The visibility barrier via private staging (FR-9/10)

Positional assembly needs slot *i* written per drain, but §2.6 forbids a partial
collection being readable. Resolution: assemble into a **host-private**
`values.Array` (on `miState`), never committed to a scope mid-run, then commit it
under `loopDataOutputRef` **once** at completion. This gives determinism (output
order = input order) and the barrier (no partial reads, no per-slot `DataChange`
facts) with one commit at the end.

### §4.4 Sequential completionCondition is "stop", not "cancel" (FR-11)

Only one instance scope is ever open for a sequential MI (the re-entry seam
serializes passes onto one child DataPath). So a `true` `completionCondition`
needs no active-scope teardown — it sets the terminal state and falls through to
the normal host-resume tail, structurally identical to Standard Loop's
`reachedMax` early-exit. ADR-025 §2.7's per-scope teardown (the ADR-018 v.1
interruption mechanism) therefore never engages for sequential MI — the §2.5
"≤ 1 open scope" invariant reduces it to a no-op. (Parallel MI, SRD-056, is where
real cancellation of concurrent instance scopes lives.)

### §4.5 Cardinality frozen at activation (FR-6)

N is read once at the first `onScopeOpen` and stored on `miState`; the runtime
never re-reads the source. This realizes §13.3.7's "the number of instances is
determined once at activation" and makes the loop deterministic even if the source
collection mutates mid-run.

## §6 Test scenarios

| Test | Level | Asserts (FR) |
|---|---|---|
| `TestMultiInstanceBuildAndAccessors` | model | FR-1 fields/accessors |
| `TestMultiInstanceRejectsBothCardinalityAndCollection` | model | FR-2 XOR |
| `TestMultiInstanceRejectsNonIntCardinality` | model | FR-2 `loopCardinality` result-type |
| `TestMultiInstanceRejectsNonBoolCompletion` | model | FR-2 `completionCondition` result-type |
| `TestMultiInstanceRequiresInputItemForCollection` | model | FR-2 collection needs `inputDataItem` |
| `TestEventSubProcessRejectsMultiInstance` | model | FR-3 inherited event-sub rejection |
| `TestMultiInstanceOf` / `TestCompositeIteratorDispatch` | instance | FR-4/FR-5 detector + shared seam |
| `TestMultiInstanceCardinalityFromExpression` | instance | FR-6 N from `loopCardinality` |
| `TestMultiInstanceCardinalityFromCollection` | instance | FR-6 N from `Count()` |
| `TestMultiInstanceZeroCardinalityCompletes` | instance | FR-6 N ≤ 0 → zero instances |
| `TestMultiInstanceRunsNSequentially` | instance | FR-7 body runs N times, one at a time |
| `TestMultiInstanceInputItemVisibleToBody` | instance | FR-8 element *i* bound + read |
| `TestMultiInstanceAssemblesOutputInOrder` | instance | FR-9 output slots = input ordinals |
| `TestMultiInstanceOutputUnpublishedMidRun` | instance | FR-10 barrier — no partial reads |
| `TestMultiInstanceCompletionConditionTruncates` | instance | FR-11 early stop, output length = completed |
| `TestMultiInstanceRuntimeAttributes` | instance | FR-12 `numberOf*` visible to the condition, incl. `numberOfActiveInstances` = 1 while an instance runs, 0 at the boundary |
| `TestMultiInstanceEmitsIterationFacts` | instance | FR-13 scope facts carry the ordinal |
| `TestMultiInstanceSequentialE2E` | thresher | FR-6–FR-11 end-to-end over a collection |

## §7 Milestones

| M | Scope | Files |
|---|---|---|
| **M1** | MI model + validation | `activities/multiinstance.go`, `subprocess.go` (inherited event-sub test) |
| **M2** | `multiInstanceOf` + shared `compositeIterator` seam (refactor Standard-Loop blocks) | `internal/instance/mi.go`, `scope_runtime.go`, `std_loop.go` |
| **M3** | Cardinality + per-instance input bind + `miState` counters | `mi.go`, `scope.go` (`bindDataItemAt`), `scope_runtime.go`, `track.go` |
| **M4** | Output staging/assembly + visibility barrier + `completionCondition` | `mi.go` |
| **M5** | e2e + `examples/multi-instance-sequential/` + docs (guide/CHANGELOG/tracker/READMEs) | `pkg/thresher/…`, `examples/…`, docs |

## §8 Cross-doc

- **Implements** ADR-025 v.1 §2.4–§2.7, §2.9 (upward; the sequential MI slice —
  §2.8 `behavior` is deferred to SRD-056, so it is not claimed here).
- **Upstream** ADR-011 v.7 (Collection mediator), ADR-010 v.2 (name resolution),
  ADR-023 v.2 (scope re-entry), ADR-013 v.2 (facts), ADR-001 v.6 (loop execution) —
  all up/sideways, version-pinned.
- **Related** SRD-054 (sideways, number-only per the one-shot rule) — the seam
  generalized here.
- No downward references.

## §9 Definition of Done

- FR-1…FR-14 wired and covered by the §6 tests; the e2e green.
- `make ci` green (diff-coverage ≥95% touched files; `-race`; govulncheck; all modules).
- `examples/multi-instance-sequential/` runs to completion (built binary gitignored).
- Conformance tracker row 4 advanced (Standard Loop ✅ + MI-sequential ✅; MI-parallel /
  `ComplexBehaviorDefinition` / `completionQuantity` remain); CHANGELOG `[Unreleased]`;
  iteration guide MI section; README EN+RU.
- `/check-srd` PASS. ADR-025 stays Accepted (no change — this SRD implements it).

## §10 Implementation summary

> ⚠️ TODO: fill AFTER landing — stage commits, empirical deltas vs this draft,
> backlog.

### §10.1 Stages by commit (branch `feat/mi-sequential`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|

### §10.2 Empirical findings vs the draft

### §10.3 Backlog

## Open questions

None.
