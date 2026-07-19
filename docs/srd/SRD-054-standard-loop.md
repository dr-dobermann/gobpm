# SRD-054 — Standard Loop

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-07-19 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-025 v.1](../design/ADR-025-activity-iteration-loop-and-multi-instance.md) §2.2–§2.3 (the Standard Loop slice of the activity-iteration model; epic #88) |
| Upstream | [ADR-010 v.2](../design/ADR-010-process-data-model.md) (the execution frame = per-execution data boundary that isolates each iteration), [ADR-023 v.2](../design/ADR-023-sub-process-and-call-activity.md) (the composite child-scope re-entry seam), [ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md) (a boundary event arms once and guards the whole loop), [ADR-001 v.6](../design/ADR-001-execution-model.md) (the loop owns node execution) |
| Refines | — |

## §1 Background

Every activity in gobpm runs **exactly once** per token that reaches it. BPMN
2.0 §13.3.6 lets an activity carry `StandardLoopCharacteristics` — a structured
`while`/`until` loop that re-runs the inner activity **sequentially** while a
boolean `loopCondition` holds. ADR-025 §2.1–§2.3 decided the conception; this SRD
lands the **first, smallest** slice of the epic (#88): Standard Loop, on both a
leaf **Task** and a **composite** (Sub-Process / Call Activity), before the two
Multi-Instance slices (SRD-055 sequential, SRD-056 parallel) follow.

The model already carries a **wired-but-empty** hook. Audit of every existing
loop artifact: the empty `LoopCharacteristics` struct
(`pkg/model/activities/loop.go:3-5`); its wiring — `WithLoop(lc
*LoopCharacteristics)` (`activity_options.go:110-125`), `activityConfig.loop`
(`:13`, `:61`), `activity.loopCharacteristics` (`activity.go:22`), the
by-reference clone (`activity.go:117`); one test usage
`WithLoop(&LoopCharacteristics{})` (`activity_test.go:49`); and doc-comment
mentions of `WithLoop` in `service_task.go:71` / `user_task.go:80`. No execution
consumes any of it today. **Decision: accommodate, not remove** — the wiring is
exactly the seam this SRD builds on, so the empty struct becomes the sealed
interface (FR-1) and the rest is reused unchanged; the only follow-on is updating
the `activity_test.go:49` empty-struct usage to a real `StandardLoop` (M1).

ADR-025 §2.2 (as amended) chose the iteration **mechanism by activity kind**:

- a **leaf Task** iterates **in place** — re-executed once per pass, each pass in
  a fresh execution frame (the frame is already the per-execution isolation
  boundary, ADR-010);
- a **composite** iterates by **re-opening its child scope per iteration** — the
  ADR-023 nested-scope re-entry seam it already runs for its body.

Both follow the activity's single outgoing flow **once**, at loop exit, and both
let a boundary event on the looped activity arm **once** and guard every
iteration.

## §2 Requirements

### Functional — the model

- **FR-1 — `StandardLoopCharacteristics` type.** `LoopCharacteristics` becomes a
  sealed marker interface; `StandardLoopCharacteristics` is a concrete
  implementation carrying `loopCondition` (a `data.FormalExpression`, the same
  boolean-expression type gateways evaluate — `gateway.go:224`), `testBefore`
  (bool, default `false`), and an optional `loopMaximum` (`*int`, nil =
  unbounded). It embeds `foundation.BaseElement` (§13.3.6 `→ BaseElement`).
- **FR-2 — construction validates all inputs.** A `NewStandardLoop(loopCondition,
  opts…)` constructor rejects a nil `loopCondition`, a `loopCondition` whose
  `ResultType() != "bool"`, and a `loopMaximum ≤ 0` — at construction, with a
  self-identifying error (per the validate-public-params rule). `WithLoop` accepts
  any `LoopCharacteristics` (interface) and keeps its existing nil guard.
- **FR-3 — `Activity.Validate` cross-check.** Validation rejects an activity
  carrying **both** a Standard-Loop and a Multi-Instance characteristic
  (ADR-025 §2.1 "at most one").
- **FR-3a — an Event Sub-Process rejects iteration.** A `triggeredByEvent`
  `SubProcess` carrying **any** `LoopCharacteristics` (Standard Loop or
  Multi-Instance) fails validation. An Event Sub-Process is *instantiated by an
  event, not by control flow* (`sub-processes.md §13.5.4`); it has no
  token-driven activation to iterate, and its multiplicity already comes from its
  trigger (a non-interrupting start fires multiple times). Because the marker
  implements the shared `LoopCharacteristics` interface, this one guard covers
  both Standard Loop (this SRD) and future Multi-Instance. **Engine
  well-formedness rule** — the spec extract is *silent* on an explicit
  prohibition (the object model places `loopCharacteristics` on `Activity`, so a
  `SubProcess` may carry it in the schema); gobpm rejects it as semantically
  meaningless for an event-instantiated handler.

### Functional — leaf-Task execution (in place)

- **FR-4 — in-place re-execution.** A leaf activity (a `NodeExecutor` that is not
  a park-node — not a `scopeHost` / Call Activity / external worker / user task)
  carrying `StandardLoopCharacteristics` is re-executed once per pass; each pass
  opens a **fresh execution frame** (`executeNode` → `openFrameAt` +
  `defer Discard`, `track.go:943-953`), so iterations are isolated with no new
  construct.
- **FR-5 — `testBefore` semantics.** `false` (default) → **post-tested**
  (`do…while`): run once, then test `loopCondition`; loop continues while true.
  `true` → **pre-tested** (`while`): test before each run, so **zero iterations**
  are possible.
- **FR-6 — `loopMaximum` cap.** When set, at most `loopMaximum` iterations run
  regardless of the condition.
- **FR-7 — single outgoing flow at exit.** The engine follows the activity's
  outgoing sequence flow **once**, only after the loop terminates (`executeNode`
  returns `nexts`; `checkFlows` follows them — `track.go:756-763` — is called once
  after the loop breaks).

### Functional — composite execution (scope per iteration)

- **FR-8 — composite re-entry.** A composite (`scopeHost`) carrying
  `StandardLoopCharacteristics` re-opens its child scope per iteration: on scope
  drain, if `loopCondition` still holds and `loopMaximum` is not reached, the host
  is re-queued for another open via the existing re-entry seam
  (`resumeScopeHost` / `scopeEntry.queue`, `scope_runtime.go:287-313`) instead of
  receiving its terminal `scopeDone`; when the loop finishes, `scopeDone` is
  delivered as today so the composite follows its single outgoing flow once.
- **FR-9 — boundary arms once.** A boundary event on the looped activity arms
  once and guards all iterations (the host stays on the node across passes; no
  `evMoved` is emitted until loop exit, so `armBoundaries` fires once —
  `loop.go` boundary (dis)arm on `evMoved`).

### Functional — `loopCounter`, observability & front door

- **FR-10 — `loopCounter`.** A 0-based per-iteration ordinal is published so the
  `loopCondition` **and** the inner activity's expressions read it by name
  (through `execEnv.Find` → frame-first resolution). It is read-only and
  engine-maintained; each iteration sees its own value (never a stale sibling's).
- **FR-11 — observability.** Emit an iteration Fact per pass (loop enter / each
  iteration with `loopCounter` / loop exit) through the ADR-013 v.2 observability
  reporter (observer-only; echo policy per the kind→level map).
- **FR-12 — front door.** A runnable `examples/standard-loop/`, the composition/
  iteration guide, `CHANGELOG.md`, the conformance tracker row, and the READMEs
  (EN + RU) reflect the new capability.

### Non-functional

- **NFR-1 — no new event kinds for the leaf path.** The leaf-Task loop is a
  bounded `for` around `executeNode` in `run()`; it introduces no `trackEvent`
  kind and no loop-goroutine round-trip.
- **NFR-2 — reuse the expression mechanism.** `loopCondition` is evaluated
  through the existing `ExpressionEngine().Evaluate(ctx, cond, env)` + `bool`
  assertion path (`gateway.go:236-247`) — no new evaluator.
- **NFR-3 — breaking-change budget.** Turning the empty `LoopCharacteristics`
  struct into an interface is a public-API break, permitted pre-1.0 (v0.9.0): the
  type is an unused stub with zero consumers. Recorded in `CHANGELOG.md` as a
  breaking note.
- **NFR-4 — coverage.** Every file this SRD creates/updates finishes at ≥95%
  diff-coverage (aim 100%), delivered with the change; `make ci` green.

## §3 Models

### §3.1 `pkg/model/activities/loop.go` — the type family

`LoopCharacteristics` turns from an empty struct into a sealed marker; the
concrete Standard-Loop type + its constructor live here (one-entity-per-file: the
MI type arrives in its own file under SRD-055/056):

```go
// LoopCharacteristics marks an activity as iterating; the concrete kind
// (Standard Loop or Multi-Instance) selects the mechanism (ADR-025 §2.2).
type LoopCharacteristics interface {
    loopKind() loopKind // sealed discriminator — implemented only in this package
}

// StandardLoopCharacteristics is a sequential while/until loop (BPMN §13.3.6).
type StandardLoopCharacteristics struct {
    foundation.BaseElement
    loopCondition data.FormalExpression // continue-while-true test (must be bool)
    testBefore    bool                  // false = post-tested (do…while), true = pre-tested (while)
    loopMaximum   *int                  // optional cap; nil = unbounded
}

func NewStandardLoop(
    loopCondition data.FormalExpression, opts ...StandardLoopOption,
) (*StandardLoopCharacteristics, error) { /* nil + bool-type + loopMaximum>0 guards */ }
```

`StandardLoopOption` closures (project option style — the house `WithXxx`
convention, self-naming, reject bad input): `WithTestBefore()`,
`WithLoopMaximum(n int)`. Accessors `LoopCondition()`, `TestBefore()`,
`LoopMaximum() (int, bool)` expose the fields to the runtime.

### §3.2 `activity.go` / `activity_options.go` — field + validation deltas

- `activity.loopCharacteristics` (`activity.go:22`) and the `WithLoop` parameter
  (`activity_options.go:111`) change type `*LoopCharacteristics` →
  `LoopCharacteristics` (interface). `WithLoop`'s nil guard
  (`activity_options.go:113-117`) and the by-reference clone (`activity.go:117`)
  are unchanged.
- `activityConfig.Validate` / `Activity.Validate` gains the loop+MI-exclusivity
  guard (FR-3). Field-level guards (nil condition, bool type, positive maximum)
  live in `NewStandardLoop` (FR-2).
- **`subprocess.go` — `validateEventSubShape`** (`subprocess.go:164`) gains the
  FR-3a guard: a `triggeredByEvent` `SubProcess` (already the event-sub entry-shape
  validator) rejects a non-nil `loopCharacteristics`. This is the natural home —
  it already enforces the event-sub well-formedness rules (ADR-023 v.2 §2.10).

### §3.3 Runtime deltas (`internal/instance/`)

- **`track.go` — the leaf loop wrapper.** In `run()` around the `executeNode`
  call (`track.go:756`), a bounded loop drives re-execution when `step.node`
  carries `StandardLoopCharacteristics` and is not a park-node
  (`checkNodeType` classes, `track.go:410-457`): pre-test (if `testBefore`),
  publish `loopCounter` into the execution frame, `executeNode`, increment,
  `loopMaximum` check, post-test (if not `testBefore`); `checkFlows(nexts)` runs
  **once** after the loop.
- **`scope_runtime.go` — the composite re-entry hook.** In `resumeScopeHost` /
  `completeScope` (`scope_runtime.go:256-313`): if the drained host carries
  `StandardLoopCharacteristics` and the loop continues, re-queue via
  `entry.queue` → `onScopeOpen` and commit the next `loopCounter` into the fresh
  child scope; otherwise deliver `scopeDone` as today.
- **`loopCounter` datum** — built like a runtime variable
  (`values.NewVariable(n)` → item definition → parameter `loopCounter`), published
  **per iteration** into the leaf frame (`frame.Put`) or the composite child scope
  (`Scope.Commit`); never routed through the instance-global `RuntimeVar` subtree
  (siblings must not see a stale counter).

## §4 Analysis

### §4.1 The hybrid mechanism realizes ADR-025 §2.2

The parent ADR §2.2 prescribes **isolation-by-frame for a leaf, isolation-by-scope
for a composite**. Grounding confirms both are cheap:

- `executeNode` already opens a **fresh frame per call** and `defer f.Discard()`s
  it (`track.go:943-953`), commits on success (`finalizeNodeExecution`,
  `track.go:978`), and — crucially — **returns `nexts` without following them**
  (the run loop's `checkFlows` follows them, `track.go:756-763`). So a `for`
  around `executeNode` re-runs the same node N times, each pass isolated by its
  own frame, and emits the outgoing flow exactly once. No new machinery (FR-4,
  FR-7; NFR-1).
- A composite is *not* run by `executeNode`; it **parks** as a `scopeHost` and
  its body runs in a child scope drained through `resumeScopeHost`
  (`scope_runtime.go:287-313`). The loop is a re-queue on that existing seam
  (FR-8). Forcing a scope onto a leaf Task instead was rejected in ADR-025 §2.2
  (a Task is not a scope container).

### §4.2 `loopCondition` reuses the existing evaluator (NFR-2)

The boolean test reuses the exact gateway path: `cond.ResultType() != "bool"`
guard, `re.ExpressionEngine().Evaluate(ctx, cond, re)`, `res.Get(ctx).(bool)`
(`gateway.go:227-247`). Standard Loop needs no new evaluator — only a
`data.FormalExpression` field and a call to that same path at each pass/drain,
against the iteration's frame/scope environment.

### §4.3 `loopCounter` publication (FR-10)

Per-node execution data lives in the **frame** (frame-first resolution via
`execEnv.Find`); scope-durable data is committed to the plane. For the leaf path
`loopCounter` is `Put` into the per-iteration execution frame before
`executeNodeCore`; for the composite path it is `Scope.Commit`ed into the fresh
child scope at re-open (alongside how the existing scope seeds bind data). It is
read-only and engine-maintained; publishing it per iteration (not once, globally)
guarantees each iteration reads its own ordinal.

### §4.4 Boundary events arm once across iterations (FR-9)

For the leaf path the track stays on the same `step.node` for every pass and
emits no `evMoved` until the loop exits, so `armBoundaries` fires **once**; for
the composite path the host parks once. Either way a boundary timer/message spans
the whole loop — the desired BPMN semantics (a per-iteration re-arm would reset a
timer every pass, which is wrong).

### §4.5 `testBefore` / `loopMaximum` edge cases

Pre-tested with an initially-false condition → **zero** executions, straight to
the outgoing flow (FR-5). `loopMaximum` is checked after each pass, bounding even
an always-true condition (FR-6); `loopMaximum ≤ 0` is rejected at construction
(a zero/negative cap is a modelling error, not "run zero times" — use a
pre-tested false condition for that).

### §4.6 An Event Sub-Process excludes iteration (FR-3a)

`LoopCharacteristics` is placed on `Activity` in the BPMN object model, so a
`SubProcess` may syntactically carry it. But an Event Sub-Process is defined by
the standard as *event-instantiated, not control-flow-reached*
(`sub-processes.md §13.5.4`), and iteration characteristics govern how a
**token-activated** activity re-executes — a concept that does not apply to a
handler fired by its trigger. An event sub-process that needs to run more than
once uses a **non-interrupting** start (which fires multiple times, §13.5.4), not
a loop marker. gobpm therefore rejects any `LoopCharacteristics` on a
`triggeredByEvent` sub-process as a well-formedness rule. The rule is stated as
an **engine choice**: the extract does not enumerate an explicit prohibition
(silence is not a mandate), but the event-sub semantics make the marker
meaningless, so validation surfaces it early rather than letting the runtime
ignore it. Because the guard tests the shared `LoopCharacteristics` interface, it
covers Multi-Instance for free when SRD-055/056 land.

## §6 Test scenarios

| Test | Level | Asserts (FR) |
|---|---|---|
| `TestStandardLoopBuildAndAccessors` | model | FR-1 fields/accessors + unset-option defaults |
| `TestStandardLoopRejectsNilCondition` | model | FR-2 nil `loopCondition` rejected |
| `TestStandardLoopRejectsNonBoolCondition` | model | FR-2 non-bool `ResultType` rejected |
| `TestStandardLoopMaximumMustBePositive` | model | FR-2 `loopMaximum ≤ 0` rejected |
| `TestActivityLoopMarkerIsSingle` | model | FR-3 one marker per activity — a later `WithLoop` replaces |
| `TestEventSubProcessRejectsLoop` | model | FR-3a event sub-process rejects any loop/MI |
| `TestStandardLoopRunsWhileConditionHolds` | instance | FR-4/FR-5/FR-10 post-tested runs while `loopCounter < 3` |
| `TestStandardLoopPreTestedZeroIterations` | instance | FR-5/FR-7 pre-tested zero passes, flow proceeds once |
| `TestStandardLoopMaximumCaps` | instance | FR-6 cap on an always-true condition |
| `TestStandardLoopOf` | instance | loop capability detection (looped vs. plain node) |
| `TestStandardLoopConditionErrorFaults` | instance | pre-tested condition error faults the instance |
| `TestStandardLoopNonBoolConditionFaults` / `TestEvalLoopCondNonBool` | instance | non-bool runtime result rejected (via fault + direct) |
| `TestStandardLoopBodyErrorFaults` | instance | a body error propagates out of the loop |
| `TestEvalLoopCondFrameError` | instance | evalLoopCond frame-open guard |
| `TestLoopedSubProcessReopensPerIteration` | instance | FR-8 composite re-opens its scope per pass |
| `TestLoopedSubProcessPreTestedZero` | instance | FR-8 pre-tested composite zero iterations, host resumes |
| `TestLoopedSubProcessMaximumCaps` | instance | FR-6 composite cap |
| `TestLoopedSubProcessEmitsIterationFacts` | instance | FR-11 scope facts carry `loopCounter` (0/1/2) |
| `TestLoopedSubProcessPreTestedConditionError` / `…ConditionErrorFaults` | instance | composite pre/post condition-error faults |
| `TestStandardLoopLeafE2E` | thresher | FR-4–FR-7/FR-10 leaf loop end-to-end |
| `TestStandardLoopSubProcessE2E` | thresher | FR-8 looped Sub-Process end-to-end |

## §7 Milestones

| M | Scope | Files | Tests |
|---|---|---|---|
| **M1** | Model + validation | `activities/loop.go` (interface + `StandardLoopCharacteristics` + `NewStandardLoop` + options), `activity.go:22`, `activity_options.go` (`WithLoop` type, `Validate` exclusivity), `subprocess.go` (`validateEventSubShape` FR-3a guard), `activity_test.go:49` (empty-struct usage → real `StandardLoop`) | the 6 model tests |
| **M2** | Leaf-Task in-place seam + `loopCounter` (leaf) | `internal/instance/track.go` (loop wrapper in `run()`, frame `Put`) | post/pre-tested, maximum, single-flow, fresh-frame, counter-visibility |
| **M3** | Composite scope re-entry + `loopCounter` (scope) + observability | `internal/instance/scope_runtime.go` (re-entry hook, `Scope.Commit`), the iteration Fact | looped-subprocess re-open, boundary-armed-once, iteration facts |
| **M4** | e2e + example + docs | `pkg/thresher/standard_loop_test.go`, `examples/standard-loop/`, guide, CHANGELOG, tracker, READMEs EN+RU | the two e2e tests |

## §8 Cross-doc

- **Implements** ADR-025 v.1 §2.2–§2.3 (upward; the Standard-Loop slice).
- **Upstream** ADR-010 v.2 (frame isolation), ADR-023 v.2 (composite re-entry),
  ADR-018 v.1 (boundary arming), ADR-001 v.6 (loop-owned execution) — all
  up/sideways, version-pinned.
- No downward references. This SRD is referenced by no higher-hierarchy doc.

## §9 Definition of Done

- FR-1…FR-12 wired and covered by the §6 tests; the two e2e tests green.
- `make ci` green (tidy, lint 0, `-race`, diff-coverage ≥95% on touched files
  per NFR-4, govulncheck clean, all modules).
- `examples/standard-loop/` runs to completion under a timeout (its built binary
  gitignored, not staged).
- ADR-025 flipped Draft → Accepted (the branch lands the conception + this first
  slice, per the ADR-023/SRD-049 precedent) + its RU twin refreshed.
- Conformance tracker row 4 (`StandardLoopCharacteristics`) advanced; CHANGELOG
  `[Unreleased]` entry (with the breaking `LoopCharacteristics` note); README
  EN+RU capability paragraph; iteration guide.
- `/check-srd` PASS.

## §10 Implementation summary

### §10.1 Stages by commit (branch `feat/standard-loop`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| M1 | `18393b0` | Model + validation: `LoopCharacteristics` interface, `StandardLoopCharacteristics`, `NewStandardLoop` + `WithTestBefore`/`WithLoopMaximum`, `activity` field/accessor, FR-3a event-sub guard | 6 model |
| M2 | `c6bfe62` | Leaf in-place seam: `internal/instance/std_loop.go` (`executeStep`/`runStandardLoop`/`evalLoopCond`, `standardLoopOf`), `bindLoopCounterAt` | 9 instance + 1 thresher e2e |
| M3 | `ffd78bc` | Composite scope re-entry: `resumeScopeHost` re-open + `onScopeOpen` pre-tested-zero, `track.loopCounter`, `bindLoopCounterOrFail`, `reportScope`+`AttrLoopCounter` | 7 instance |
| M4 | `b75539f` | e2e, `examples/standard-loop/`, `docs/guides/iteration.md`, CHANGELOG, tracker, READMEs EN+RU | 1 thresher e2e |

### §10.2 Empirical findings vs the draft

- **Leaf loop factored into its own file.** §3.3 sketched the wrapper inline in
  `track.go` `run()`; it landed as `internal/instance/std_loop.go`
  (`executeStep`/`runStandardLoop`) called from `run()` — same behaviour, cleaner
  separation.
- **Single test-site.** `runStandardLoop` uses one condition-test position
  (`TestBefore() || loopCounter > 0`) for both pre- and post-tested loops, rather
  than the two the §2.3 prose implies — fewer branches, one bind per pass.
- **`loopCounter` binding.** Published to the enclosing scope via
  `bindLoopCounterAt` (both condition and inner activity resolve it by walk-up),
  not a frame `Put`; the composite's two bind sites were consolidated into
  `bindLoopCounterOrFail`.
- **Untriggerable defensive wraps.** `bindLoopCounterAt` → `plane.Commit` does
  **not** fail on a non-existent path (it lazily accepts), so its error wrap is
  dead-defensive (accepted, the `evalCondition` class). covercheck counts *source
  lines* per uncovered block, which drove the single-bind + helper consolidation
  to keep the diff-coverage gate green (95.2% of 229).
- **Non-bool guard.** `evalLoopCond`'s non-boolean-result branch is unreachable
  through `goexpr` (it enforces the declared result type at `Evaluate`), so it is
  covered directly with a mock `FormalExpression`.
- **FR-9 (boundary arms once)** has no dedicated named test — it rests on the
  host-parks-once / leaf-stays-on-node structural argument; a dedicated
  boundary-across-iterations test is a §10.3 follow-up.

### §10.3 Backlog

- **Multi-Instance** — the rest of ADR-025: SRD-055 (sequential) and SRD-056
  (parallel + `behavior`/`ComplexBehaviorDefinition`), which will exercise the
  same scope substrate.
- A **boundary-arms-once-across-iterations** regression test (FR-9), currently
  covered only structurally.

## Open questions

None.
