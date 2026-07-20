# SRD-055 — Multi-Instance (sequential)

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-07-20 |
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
  = element *i* of the `loopDataInputRef` collection at the **host (enclosing)
  scope** — exactly where `loopCounter` binds (a new `bindDataItemAt`, modeled on
  `bindLoopCounterAt`); the body reads it by name via scope walk-up. Binding at
  the enclosing scope is safe for a sequential MI (one instance at a time, each
  pass rebinds before opening) and mirrors the Event-Sub-Process payload
  precedent.
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

- **`compositeIterator`** (interface) + the dispatch in `scope_runtime.go`. As
  landed the seam has **three** per-pass hooks (the output capture needs a point
  before the child scope closes): `firstOpen(host)` (resolve N once, bind the
  first instance; report a zero-iteration skip), `beforeClose(host, childPath)`
  (capture the draining instance's output item — a no-op for Standard Loop), and
  `afterDrain(host) (reopen bool)` (advance, test the terminal condition +
  `completionCondition`, bind the next instance / publish the output).
  `standardLoopOf` and `multiInstanceOf` return strategies; the
  `onScopeOpen`/`resumeScopeHost` blocks collapse to one `compositeIteratorOf(node)`
  dispatch.
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

Landed test names (reconciled to the implementation at Accepted).

| Test | Level | Asserts (FR) |
|---|---|---|
| `TestMultiInstanceBuildAndAccessors` | model | FR-1 fields/accessors |
| `TestMultiInstanceRejectsBothCardinalityAndCollection` / `…RejectsNoCardinalitySource` | model | FR-2 XOR (both / neither) |
| `TestMultiInstanceRejectsNonIntCardinality` | model | FR-2 `loopCardinality` result-type |
| `TestMultiInstanceRejectsNonBoolCompletion` | model | FR-2 `completionCondition` result-type |
| `TestMultiInstanceRequiresInputItemForCollection` | model | FR-2 collection needs `inputDataItem` |
| `TestMultiInstanceOptionGuards` | model | FR-2 option nil-guards |
| `TestEventSubProcessRejectsMultiInstance` | model | FR-3 inherited event-sub rejection |
| `TestCompositeIteratorDispatch` | instance | FR-4/FR-5 detector + shared seam |
| `TestMultiInstanceRunsNSequentially` | instance | FR-6 (N from `loopCardinality`) + FR-7 (runs N times, one at a time) |
| `TestMultiInstanceCardinalityFromCollection` | instance | FR-6 N from `Count()` |
| `TestMultiInstanceZeroCardinality` | instance | FR-6 N ≤ 0 → zero instances |
| `TestMultiInstanceNonIntCardinality` / `…CardinalityEvalError` / `…NonCollectionRef` / `…MissingCollectionRef` | instance | FR-6 cardinality error paths |
| `TestMultiInstanceInputItemVisible` | instance | FR-8 element *i* bound + read |
| `TestMultiInstanceAssemblesOutput` / `…OutputItemMissing` | instance | FR-9 output slots = input ordinals (+ missing-item fault) |
| `TestMultiInstanceOutputUnpublishedMidRun` | instance | FR-10 barrier — invisible mid-run, published at completion |
| `TestMultiInstanceCompletionConditionTruncates` / `…NonBoolCompletion` | instance | FR-11 early stop (+ non-bool fault) |
| `TestMultiInstanceRuntimeCounters` | instance | FR-12 `loopCounter`/`numberOfInstances`/`numberOfActiveInstances` (=1)/`numberOfCompletedInstances` per pass |
| `TestMultiInstanceEmitsIterationFacts` | instance | FR-13 scope facts carry the ordinal |
| `TestParallelMultiInstanceRejected` | instance | parallel-MI gap gate (→ SRD-056) |
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

Landed on branch `feat/mi-sequential`; `make ci` green at every milestone
(lint 0, `-race`, govulncheck clean, all modules), diff-coverage ≥95% touched.

### §10.1 Stages by commit (branch `feat/mi-sequential`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| SRD | `4c4ff03` | this document (Draft) | — |
| M1 | `c5a60a9` | MI model + validation (`multiinstance.go`; `resultTypeBool`/`resultTypeInt` consts) | 8 model |
| M2 | `cf87e86` | shared `compositeIterator` seam + cardinality (`composite_iter.go`, `mi.go`, `std_loop.go` strategy, `scope_runtime.go` dispatch; `bindLoopCounterOrFail` removed) | 9 instance |
| M3 | `791ff3a` | per-instance input data + `numberOf*` counters (`bindDataItemAt`, `resolveActivation`, `bindInstance`, `track.miState`) | +2 instance |
| M4 | `6ea63b1` | output assembly + `completionCondition` (`beforeClose`, `publishOutput`, `evalCompletion`, `bindValueAt`) | +5 instance |
| M5-A | `aa1e37b` | thresher e2e + `examples/multi-instance-sequential/` + docs (guide/CHANGELOG/tracker/READMEs) | +1 e2e |
| Canaries | `8caf37e` | FR-10/12/13 canaries + `scopeLoopCounter` MI fix | +2 instance |

### §10.2 Empirical findings vs the draft

- **Cardinality folded into M2.** The §7 plan put cardinality resolution in M3;
  it landed in M2 (both sources), so M2 already runs an MI N times. M3 became the
  input-data mediator + counters, M4 the output + completion — each milestone
  independently runnable.
- **Three seam hooks, not two.** §3.2 sketched `firstPass`/`nextPass`. Output
  capture must read the child scope **before** `CloseScope` (`completeScope`
  closes it before `resumeScopeHost`), so a third hook `beforeClose(host,
  childPath)` was added (a no-op for Standard Loop). Reconciled in §3.2.
- **Per-instance data at the host (enclosing) scope, not the child.** "Exactly
  like `loopCounter`" resolves to `host.scopePath`; the body reads via walk-up.
  Mirrors the SRD-053 payload-at-enclosing precedent (a child-scope commit-error
  guard is uncoverable). Reconciled in FR-8.
- **FR-13 gap caught at `/check-srd`.** `scopeLoopCounter` recognized only a
  Standard Loop, so MI scope facts carried no ordinal; fixed to gate on the
  shared `compositeIterator` (`8caf37e`).
- **`bindLoopCounterAt` generalized.** M3 routed it through the new
  `bindDataItemAt` (`loopCounter` is now a `Variable[any]`); M4 split
  `bindValueAt` (commits a `data.Value` verbatim) for the output collection.
- **Residual uncovered lines** are untriggerable-defensive (`bindDataItemAt` /
  `openFrameAt` error returns — `bindLoopCounterAt` lazy-creates, so it never
  fails; the `evalLoopCond`-class), the same class SRD-054 shipped.

### §10.3 Backlog

- Parallel Multi-Instance + `behavior`/`ComplexBehaviorDefinition` + the voting
  use-case example → **SRD-056**.
- `completionQuantity` → deferred (SRD-046 NFR-4).
- MI compensation → future Transaction work (ADR-025 §2.10).

## Open questions

None.
