# SRD-055 — Multi-Instance (sequential)

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-07-21 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-025 v.2](../design/ADR-025-activity-iteration-loop-and-multi-instance.md) §2.4–§2.7, §2.9 (the Multi-Instance model — the **sequential** slice; §2.8 `behavior` → SRD-056.B), §2.12 (composite iteration as an off-loop decorator); epic #88 |
| Upstream | [ADR-017 v.1](../design/ADR-017-channel-based-event-processing.md) (the single-writer loop the decorator requests scope operations from), [ADR-011 v.7](../design/ADR-011-process-data-flow.md) (the structural `data.Collection` — `Count`/`GetAt`/`SetAt` — the split/assemble mediator rides), [ADR-010 v.2](../design/ADR-010-process-data-model.md) (name-based data-plane resolution), [ADR-023 v.2](../design/ADR-023-sub-process-and-call-activity.md) (the composite child-scope lifecycle), [ADR-013 v.2](../design/ADR-013-instance-observability.md) (iteration facts), [ADR-001 v.6](../design/ADR-001-execution-model.md) |
| Refines | — |
| Related | SRD-054 (the iteration decorator this reuses and extends) |

## §1 Background

BPMN 2.0 §13.3.7 lets an activity carry `MultiInstanceLoopCharacteristics` — run
the inner activity **N times** (fixed at activation, from an int `loopCardinality`
XOR a collection's size), splitting a collection element in per instance and
assembling one out, with a `completionCondition` for early stop. This SRD lands the
**sequential** shape (one instance at a time).

**This is the second slice of the ADR-025 v.2 decorator re-landing** (§2.12,
after SRD-054's composite Standard Loop). The prior landing drove sequential-MI
iteration *control* on the **loop goroutine** via the `compositeIterator`/`miIterator`
seam (`firstOpen`/`beforeClose`/`afterDrain` called from `onScopeOpen`/`completeScope`/
`resumeScopeHost`). §2.12 moved that control onto the activity's own **off-loop
execution** — the iteration decorator. SRD-054 landed the engine (the `scopeReq`
request/response scope protocol + `runCompositeLoop`); this SRD re-lands sequential
MI on it as `runMISequential`, so the `compositeIterator` seam retires entirely.

This SRD **deletes and reuses** the SRD-055 slot: its prior (loop-goroutine-driven)
execution content is replaced with the decorator design; the **model and the data
mediator / completion / attribute semantics are unchanged** — only *who drives* the
iteration moves off the loop.

**The one forced loop-side operation — output capture.** The `outputDataItem` of a
completed instance must be read from its child scope **before that scope closes**
(the visibility barrier assembles it into a private staging collection). The
decorator only wakes **after** the drain (its `awaitScopeDrained` is released by the
`scopeDone` the loop delivers *post-`CloseScope`*), and `Scope.GetData` can only read
a scope that is still open. So the capture — and only the capture — stays a loop-side
step in `completeScope`, symmetric with parallel MI's existing `captureParallelOutput`
(SRD-056.A); the decorator drives everything else off the loop. The
`scopeDone`-on-`evtCh` send is the happens-before fence: the loop captures before it
emits `scopeDone`, so the staged output is complete when the runner wakes; because MI
is **sequential**, one instance's staging slot is in flight at a time and the publish
barrier is after the last drain — no concurrent reader/writer.

## §2 Requirements

### Functional — the model (landed, unchanged)

- **FR-1 — `MultiInstanceLoopCharacteristics` type.** A `LoopCharacteristics`
  implementation (`activities/multiinstance.go`) carrying `isSequential`,
  `loopCardinality` (int expr, nil ⇒ collection-driven), `loopDataInputRef` /
  `loopDataOutputRef` / `inputDataItem` / `outputDataItem` (**string names**), and
  `completionCondition` (bool expr, nil ⇒ run all N); embeds `foundation.BaseElement`.
- **FR-2 — construction validates all inputs.** `NewMultiInstance(opts…)`: XOR of
  `loopCardinality` / `loopDataInputRef`; `loopCardinality.ResultType()=="int"`;
  `completionCondition.ResultType()=="bool"`; a collection-driven MI requires
  `inputDataItem`. Self-identifying errors.
- **FR-3 — Event Sub-Process rejects MI (inherited).** Covered by SRD-054 FR-3a
  (the shared `LoopCharacteristics` guard); a test asserts it holds for the MI marker.

### Functional — the decorator drives sequential MI off the loop (reworked)

- **FR-4 — `multiInstanceOf` detector.** The capability detector (`mi.go`, sibling
  to `standardLoopOf`) reports the MI characteristics; mutually exclusive with
  `standardLoopOf`.
- **FR-5 — the host drives its own iteration via `runMISequential`.** A sequential-MI
  composite iterates on its **own runner goroutine** (a new `runMISequential`,
  routed by `executeStep` before the park path, like `runCompositeLoop`). It resolves
  N once, then for each instance opens the child scope (`scopeRoundtrip`), parks for
  the drain (`awaitScopeDrained`), evaluates the completion condition, and on exit
  publishes the assembled output and follows the outgoing flow once. The host no
  longer **parks for control** — a `drivesOwnIteration(node)` helper (Standard-Loop
  composite **or** sequential-MI composite) makes `checkNodeType`/`enterComposite`
  not-park it. **Parallel MI still parks** (its fan-out driver is a separate slice).
- **FR-6 — cardinality once at activation.** N is resolved **exactly once**, off the
  loop, at the top of `runMISequential`: `loopCardinality` evaluated to an `int` (the
  transient frame + `Evaluate` path `resolveActivation` already uses), **or** the referenced
  collection's `Count()`. Frozen for the activity's lifetime (§13.3.7). N ≤ 0
  completes with zero instances (no scope opened, no publish, follow outgoing once).
- **FR-7 — sequential re-entry.** Instance *i+1*'s scope opens only after instance
  *i*'s has drained — the decorator's `for i := 0; i < N` loop awaits each drain
  before the next `scopeRoundtrip`. At most one instance scope is ever open.

### Functional — the data mediator (semantics unchanged; loci per §2.12)

- **FR-8 — split in (off the loop).** Before instance *i*'s scope opens, the
  decorator binds `inputDataItem` = element *i* of the `loopDataInputRef` collection
  at the **host scope** (`bindDataItemAt`, a mutex-safe plane write, off the loop —
  like SRD-054's off-loop `bindLoopCounterAt`); the body reads it by name via
  walk-up. The bind lands **before** `scopeRoundtrip`, so the seeded body sees it.
- **FR-9 — assemble out (capture loop-side, assemble off-loop).** When instance *i*
  drains, its `outputDataItem` is read from the still-open child scope into slot *i*
  of a **private staging** collection — this **capture stays on the loop goroutine**
  (`captureSequentialOutput` in `completeScope`, before `CloseScope`), the one
  operation the decorator cannot do (§1, §4.2). The staging is host-owned; the loop's
  per-pass `SetAt(i,…)` write is fenced before the `scopeDone` the decorator awaits.
- **FR-10 — visibility barrier.** The staging collection is **not** scope-visible
  during the run (no per-slot commit, no partial reads, no per-slot `DataChange`);
  the decorator publishes it under `loopDataOutputRef` at the host scope **once**
  (`bindValueAt`, off the loop), at activity completion. A never-run instance leaves
  its slot at its pre-run value.

### Functional — completion condition & runtime attributes (semantics unchanged)

- **FR-11 — `completionCondition`.** Evaluated off the loop after each instance
  drains (`evalCompletion` — its existing `openFrameAt` + `Evaluate` + bool-assert):
  `true` → the activity is **done now** — no further
  instance launches, the output publishes, the host follows its outgoing; `false` →
  the next instance launches (or the activity completes when all N are done). For
  **sequential** MI, "cancel remaining" is simply "stop launching" — one instance
  runs at a time, so there is **no** active-scope teardown. The decorator increments
  `numberOfCompletedInstances` and rebinds the §2.9 attributes **before** the
  evaluation, preserving the exact count the prior landing exposed to the condition.
- **FR-12 — runtime attributes.** A per-host `miState` (frozen N, completed count,
  the staging) drives `numberOfInstances` / `numberOfCompletedInstances` /
  `numberOfActiveInstances` (0 or 1 for sequential), bound at the host scope off the
  loop alongside `loopCounter` each pass so `completionCondition` and the body resolve
  them by name. `miState` becomes **host-runner-owned** (the decorator drives it),
  except the single loop-side `SetAt` capture (FR-9).

### Functional — observability & front door

- **FR-13 — observability.** Each instance's scope facts carry the iteration ordinal
  — `scopeLoopCounter` keys on `drivesOwnIteration(node)` so a decorator-driven
  sequential MI still reports `loopCounter` (the same fix SRD-054 M2 made for SL).
- **FR-14 — front door.** `examples/multi-instance-sequential/`, the iteration guide,
  `CHANGELOG.md`, the conformance tracker, the READMEs (EN + RU) already describe
  sequential MI (behavior unchanged); the rework needs only a CHANGELOG note.

### Non-functional

- **NFR-1 — reuse the decorator, don't duplicate.** No new scope-lifecycle / seeding
  / drain / protocol code — `runMISequential` reuses the landed `scopeRoundtrip` /
  `awaitScopeDrained` / `handleScopeRequest` / `executeNode`. The genuinely new code
  is `runMISequential` + `captureSequentialOutput` (a relocation of `beforeClose`) +
  the `drivesOwnIteration` helper.
- **NFR-2 — single-writer preserved (ADR-017 v.1).** The loop stays the sole writer
  of scope lifecycle (`OpenScope`/`CloseScope`, `ls.scopes`, `ls.waiting`) and
  performs the one pre-close plane **read** (the capture). The decorator does only
  mutex-safe plane **writes** (`bindDataItemAt`/`bindValueAt`) off the loop.
- **NFR-3 — the staging fence is the one deliberate cross-goroutine field.** Written
  on the loop (the per-pass capture) and read off-loop at publish, ordered by the
  `scopeDone`-on-`evtCh` edge; safe **only** because MI is sequential (one slot in
  flight, barrier at end). Documented on `miState`; `-race` on the e2e guards it.
- **NFR-4 — deferred surfaces stay out.** No `behavior` (SRD-056.B), no parallel
  execution (SRD-056.A untouched), no compensation.
- **NFR-5 — the existing suites are the safety net.** The landed `TestMultiInstance*`
  (`internal/instance/mi_test.go`) + `pkg/thresher/mi_sequential_test.go` stay green
  throughout — behavior is unchanged.
- **NFR-6 — coverage.** Every touched file finishes ≥95% diff-coverage (aim 100%);
  `make ci` green.

## §3 Models

### §3.1 Model type (`pkg/model/activities/multiinstance.go`) — landed, unchanged

`MultiInstanceLoopCharacteristics` (embedding `foundation.BaseElement`) with the
FR-1 fields, built by `NewMultiInstance(opts…)` under the FR-2 guards. **No model
change in this SRD** — the rework is runtime-only.

### §3.2 Runtime deltas (`internal/instance/`) — the decorator drives MI

- **`mi.go` — `runMISequential` (new, host runner) + `miState` relocation.**
  ```
  runMISequential(ctx, step, mi):
    n, col := resolveActivation(...)             // off-loop; count once (FR-6)
    host.miState = &miState{n, col, staging?, names}   // off-loop
    if n <= 0 { host.miState = nil; return t.executeNode(ctx, step) }   // zero-instance
    for i := 0; i < n; i++ {
       bindInstance(ctx, host, i)                // off-loop: loopCounter=i, numberOf* attrs, inputItem=col.GetAt(i) (FR-8/12)
       scopeRoundtrip{host, node}                // loop opens child scope, seeds, arms (reuse handleScopeRequest)
       awaitScopeDrained(ctx)                    // loop runs captureSequentialOutput + CloseScope, then scopeDone (FR-9)
       host.miState.completed++                  // off-loop
       rebindCounters(host)                      // off-loop: numberOfCompletedInstances now current (FR-11)
       if mi.CompletionCondition() != nil && evalCompletion(...) { break }   // stop-launching (FR-11)
    }
    publishOutput(host)                          // off-loop: bindValueAt(host.scopePath, outputRef, staging) — barrier (FR-10)
    host.miState = nil
    return t.executeNode(ctx, step)              // follow the composite's single outgoing once (FR-7)
  ```
  `resolveActivation`, `bindInstance`, `evalCompletion`,
  `publishOutput` — **kept**, their callers move to the runner (off-loop).
  `resolveActivation` stays reused by parallel MI's `fanOutParallelMI` (untouched).

- **`scope_runtime.go` — `captureSequentialOutput` (the one loop-side step) + seam
  rewire.** `beforeClose`'s capture (`GetData(childPath, outputItem)` →
  `staging.SetAt(loopCounter, …)`) is relocated into a plain `captureSequentialOutput`
  called from `completeScope` before `CloseScope`, symmetric with
  `captureParallelOutput`. `resumeScopeHost`'s top-guard generalizes from
  `standardLoopOf` to `drivesOwnIteration` (a sequential-MI drain just
  `dispatchToParked(scopeDone)` to the parked runner, no `afterDrain`).
  `scopeLoopCounter` keys on `drivesOwnIteration`.

- **`track.go` / `std_loop.go` — the interception.** `drivesOwnIteration(node)` =
  SL-composite OR sequential-MI-composite; `enterComposite` returns nil (no park) for
  it; `executeStep` routes a `scopeHost` sequential MI to `runMISequential`.

- **Seam removal (M2).** `miIterator.firstOpen`/`afterDrain`, the `compositeIterator`
  interface + `compositeIteratorOf`, and the `onScopeOpen`/`resumeScopeHost`
  sequential-MI branches are deleted (both SL and sequential MI now leave the seam).

## §4 Analysis

### §4.1 `runMISequential` realizes ADR-025 v.2 §2.12

The decorator drives the count-driven iteration on the host runner, requesting scope
opens from the single-writer loop via the SRD-054 protocol — the locus §2.12
dictates. It is a **separate driver** from `runCompositeLoop` (not a strategy hook):
MI is count-driven (N once) vs condition-driven; carries per-instance data + output
assembly; and its condition is a *completion* (test-after-body, early-stop) not a
*continuation* (test-before-body). The only shared primitives — `scopeRoundtrip`,
`awaitScopeDrained`, `executeNode` — are already small standalone `*track` helpers;
a shared skeleton for two divergent bodies would be more branching than sharing.

### §4.2 The capture stays loop-side — the only viable option (FR-9)

`captureSequentialOutput` reads the child scope's `outputDataItem` and must run in the
window between the inner tracks finishing and `CloseScope` — a window **only the loop
observes** (it is what triggers `completeScope`). A capture *request* is impossible:
the decorator is parked in `awaitScopeDrained` and learns of the drain only via the
`scopeDone` delivered **after** `CloseScope`; an off-loop capture at
`scopeRoundtrip`-return is too early (the body has not run). So the capture is
loop-side by necessity, not preference — and it is symmetric with the parallel MI
capture already living in `completeScope`. Safety: the loop's `SetAt(i,…)` happens
before it emits `scopeDone` (`dispatchToParked` → `evtCh <- scopeDone`), which happens
before the runner's `awaitScopeDrained` receive; sequential execution means one slot
is written at a time and the publish read is after the last drain — no overlap.

### §4.3 The completion-condition sees the current count (FR-11)

`completionCondition` reads the §2.9 attributes at the host scope. The decorator
increments `completed` and rebinds `numberOfCompletedInstances` (and the running
attrs) **before** `evalCompletion`, so the condition sees the *post-drain* count —
the exact value the prior `afterDrain`/`bindInstance` sequencing exposed. Preserving
this ordering keeps the completion tests (e.g. the `numberOfCompletedInstances >= k`
scenarios) identical.

### §4.4 Zero-instance and the visibility barrier (FR-6/FR-10)

`n ≤ 0` opens no scope and follows the composite's outgoing once (`executeNode`), with
no publish (staging is unallocated). A completing activity publishes the staging under
`loopDataOutputRef` once, off the loop, after the last drain — the single visibility
barrier; no intermediate slot is scope-visible.

## §6 Test scenarios

| Test | Level | Covers |
|---|---|---|
| `TestMultiInstanceRunsNSequentially` | instance | FR-5/FR-7 — N passes, one scope at a time (existing, green) |
| `TestMultiInstanceZeroCardinality` | instance | FR-6 — n≤0, no scope, follow outgoing (existing, green) |
| `TestMultiInstanceCardinalityFromCollection` | instance | FR-6 — count = collection size (existing, green) |
| `TestMultiInstanceInputItemVisible` | instance | FR-8 — per-instance input split visible to the body (existing, green) |
| `TestMultiInstanceAssemblesOutput` | instance | FR-9/FR-10 — positional staging → published collection (existing, green; `-race`) |
| `TestMultiInstanceRuntimeCounters` | instance | FR-12 — numberOf* attributes (existing, green) |
| completionCondition tests | instance | FR-11 — stop-launching on true (existing, green) |
| cardinality/collection error tests | instance | FR-6 — eval/type/missing-ref errors (existing, green) |
| `TestMultiInstanceSequentialE2E` | thresher | FR-5–FR-10 end-to-end, ordering `[2,3,4]` (existing, green) |

Grep before M2: any test asserting on `miIterator`/`compositeIterator` internals is
re-pointed at `runMISequential` / observable behavior; the outcomes (order, counts,
output collection, errors) stay identical.

## §7 Milestones

| # | Scope | Files |
|---|---|---|
| **M1** | `runMISequential` + `drivesOwnIteration`; wire `enterComposite` (not-park) + `executeStep` (route); relocate `beforeClose` → `captureSequentialOutput` in `completeScope`; generalize the `resumeScopeHost` guard + `scopeLoopCounter`. `compositeIteratorOf` kept temporarily. `TestMultiInstance*` + e2e green on the decorator. | `mi.go`, `scope_runtime.go`, `track.go`, `std_loop.go` |
| **M2** | Remove the dead seam — `miIterator.firstOpen`/`afterDrain`, the `compositeIterator` interface + `compositeIteratorOf`, the `onScopeOpen`/`resumeScopeHost` sequential-MI branches (mirror of SRD-054 M2). | `mi.go`, `composite_iter.go`, `scope_runtime.go` |
| **M3** | CHANGELOG note; SRD-055 §10; doc sync. | docs |

## §8 Cross-doc

- **Implements** [ADR-025 v.2](../design/ADR-025-activity-iteration-loop-and-multi-instance.md) §2.4–§2.7, §2.9, §2.12.
- **Upstream** [ADR-017 v.1](../design/ADR-017-channel-based-event-processing.md), [ADR-011 v.7](../design/ADR-011-process-data-flow.md), [ADR-010 v.2](../design/ADR-010-process-data-model.md), [ADR-023 v.2](../design/ADR-023-sub-process-and-call-activity.md), [ADR-013 v.2](../design/ADR-013-instance-observability.md), [ADR-001 v.6](../design/ADR-001-execution-model.md).
- **Related** SRD-054 (the decorator engine reused). Direction: SRD → ADR / SRD only (up/sideways), version-pinned; no downward reference.

## §9 Definition of Done

- FR-1…FR-14 wired; FR-5/FR-6/FR-7 via `runMISequential`, FR-8/FR-10 off-loop,
  FR-9 via the loop-side `captureSequentialOutput`.
- §6 tests exist and pass; the landed sequential-MI suite + e2e stay green (NFR-5);
  `examples/multi-instance-sequential/` runs and exits 0.
- The `compositeIterator` seam is removed (M2); parallel MI (SRD-056.A) is unaffected.
- Single-writer preserved (NFR-2); the staging fence documented (NFR-3) and `-race`
  green on the assembly e2e.
- `make ci` green (verify the gate's own completion markers, not a wrapper exit);
  CHANGELOG `[Unreleased]` notes the internal rework.
- `/check-srd` PASS before flipping status; ADR-025 v.2 stays Draft until the whole
  re-landing (parallel MI + behavior) completes.

## §10 Implementation summary

### §10.1 Stages by commit (branch `feat/mi-sequential-decorator`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| doc | `a944451` | SRD-055 rewritten (delete-and-reuse) for the off-loop decorator | — |
| M1 | `a43c882` | `runMISequential` + `drivesOwnIteration` (`mi.go`); `executeStep` routing (`std_loop.go`); `enterComposite` not-park (`track.go`); relocate `beforeClose` → `captureSequentialOutput`, generalize the `resumeScopeHost` guard + `scopeLoopCounter` (`scope_runtime.go`); `TestRunMISequential{RequestError,BindError,DrainError}` + `miState` doc | 3 new white-box + landed `TestMultiInstance*` + e2e green |
| M2 | `a63e4f4` | remove the dead seam — `composite_iter.go` (interface + `compositeIteratorOf`), `miIterator.firstOpen`/`afterDrain`, the `onScopeOpen`/`resumeScopeHost` MI branches; `TestCompositeIteratorDispatch` → `TestDrivesOwnIteration` | rewrote 1 dispatch test; `make ci` diff-coverage 97.7% |

### §10.2 Empirical findings vs the draft

- **The capture stayed exactly where §4.2 predicted.** `captureSequentialOutput`
  lives loop-side in `completeScope` (before `CloseScope`), symmetric with parallel
  MI's `captureParallelOutput` — no surprise; the `scopeDone`-on-`evtCh` fence held
  and `-race` on the assembly e2e is clean.
- **No fold step was needed (unlike SRD-054).** SRD-054's protocol landed with no
  caller until its runner arrived; here M1's `runMISequential` **is** the runner, so
  the routing and the driver landed together — the seam went unreachable at M1 and was
  deleted at M2, no intermediate dead-caller stage.
- **The `bindInstance` fail-fast guard is reachable only past the property clone.**
  A broken input collection seeded as a process **property** is deep-copied by the
  snapshot→instance clone (its overridden `GetAt` is lost), so the guard never fired
  from a property fixture. `TestRunMISequentialBindError` injects the broken collection
  straight into the running scope via `sc.bindValueAt` (the data plane returns data by
  reference, no clone) to exercise the `col.GetAt(i)`-error path white-box — confirming
  the guard is a real fail-fast, not unreachable defense.

### §10.3 Backlog

- **Parallel MI on the decorator (SRD-056.A re-land)** and **`behavior` (SRD-056.B)**
  are the remaining ADR-025 v.2 §2.12 slices; ADR-025 v.2 flips Accepted when they land.
- One shadowed `publishOutput`-error return remains uncovered (97.7%, above the 95%
  gate) — a defensive propagation behind the already-covered `staging == nil` no-op;
  no white-box hook worth a fixture.

## Open questions

None.
