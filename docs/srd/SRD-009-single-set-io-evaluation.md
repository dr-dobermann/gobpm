# SRD-009 — Single-set I/O evaluation

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-06-13 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-011 v.2 Process Data Flow](../design/ADR-011-process-data-flow.md) |
| Refines | [ADR-010 v.1 Process Data Model](../design/ADR-010-process-data-model.md) |

This SRD lands [ADR-011 v.2](../design/ADR-011-process-data-flow.md) §2.2–§2.5 + the §2.7 "no `Set` type" decision: it **drops the reified `Set` type**, makes required/optional/while-executing **per-parameter attributes**, and adds the **runtime availability gates** — required inputs must be available at start (else a fail-fast error, never a wait) and required outputs must be produced at completion (else an error). It builds on the data-flow machinery SRD-007 already landed (the execution frame, data associations, the load→commit protocol); it does **not** re-implement association execution.

## 1. Background & motivation

### 1.1 Current state (verified against the code)

- **The execution machinery already exists** (SRD-007 / ADR-010). `Association.calculate` (`pkg/model/data/association.go:246`) does transformation/single-source copy; `task.LoadData` (`pkg/model/activities/task.go:84`) runs the **input** associations and fills the frame; `task.UploadData` (`task.go:190`) runs the **output** associations and commits; the frame load→commit/discard protocol is live (`internal/scope/frame.go`, the `NodeDataConsumer`/`NodeDataProducer` steps `track.go:641-667`); events bind data the same way (`pkg/model/events/event.go:245-389`). The runtime reads I/O **only** through `IoSpec.Parameters(dir)` (`io_spec.go:109`).
- **`Set` is a reified type that, with one set per activity, carries no information.** `Set` (`io_spec_obj.go:99`) has 12 methods (`io_spec_obj.go:146-397`) and holds `values map[SetType][]*Parameter`. The `InputOutputSpecification` already holds `params map[Direction][]*Parameter` **and** `sets map[Direction][]*Set` (`io_spec.go:86`) — the per-set parameter membership duplicates `params`. The runtime never references `data.Set`; only construction does (`activities/activity_options.go`, `events/*_options.go`, `events/event.go`/`end.go`, `examples/process-data`).
- **`SetType` buckets stand in for the per-parameter optional/while-executing flags.** `SetType` (`io_spec.go:15`) is a bit-flag enum (`DefaultSet`/`OptionalSet`/`WhileExecutionSet`, `io_spec.go:18-26`); a parameter's "role" is its `SetType` membership inside a `Set`. There is **no `optional` / `whileExecuting` attribute on `Parameter`** (`Parameter` is `{name, ItemAwareElement}`, `io_spec_obj.go`).
- **`Set.Link`/`Unlink`/`linkedSets` is dead code** — defined at `io_spec_obj.go:298-340`, referenced by nothing outside its own tests (the only "multi-set/IORule-ish" machinery).
- **No availability gate at start; no required-output check at completion.** `task.LoadData` executes input associations and errors *implicitly* if a source isn't `Ready` (`association.go:258`); it does not check "the activity's required inputs are available" as a first-class gate, and it has no notion of an *optional* input legitimately absent. `task.UploadData` pushes output associations but does not verify that **required** outputs were actually produced. The construction-time `Set.Validate` "are the default params Ready?" check (`io_spec_obj.go:356`) is the §10.4.2 stand-in ADR-011 §1.2 names — it runs at build, not as the runtime gate.

### 1.2 Why

[ADR-011 v.2](../design/ADR-011-process-data-flow.md) decides: exactly one input/output set per activity, with required/optional/while-executing as **per-parameter attributes** and **no `Set` type** (§2.2, §2.7); availability gates the start but never waits (§2.3); a required output not produced is an error (§2.2). The model machinery is in place; what is missing is (a) the model simplification — drop `Set`, flag the parameters — and (b) the runtime gates that turn "run the associations and hope" into "require the required, permit the optional, fail fast on the missing." This SRD lands both.

## 2. Goals & scope

### 2.1 Goals (in scope)

- **G1.** `Parameter` carries `optional` and `whileExecuting` booleans (default `false` = required, not while-executing). Required/optional/while-executing is a per-parameter attribute, not set membership.
- **G2.** The reified `Set` type is removed: `Set`, `SetType`, `allTypes`, `AllSets`, `Set.Link`/`Unlink`/`linkedSets`. `InputOutputSpecification` owns its `Parameter`s directly (drops the `sets` field and `AddSet`/`RemoveSet`/`Sets`) and exposes `InputSet()`/`OutputSet()` as views over its input/output parameter lists.
- **G3.** Activity and event construction declare inputs/outputs as **flagged parameters**, not sets — the `WithSet`/`WithEmptySet`/`setDef` machinery and the `inputSet`/`outputSet` `*data.Set` fields are replaced.
- **G4.** **Start-gate (inputs).** When an activity/throw-event is ready, every **required** input must be available; an unavailable required input is a **fail-fast error/incident — never a wait** (§2.3). An **optional** input may be absent (its association is skipped, the input stays `Unavailable`).
- **G5.** **Completion-gate (outputs).** At completion, every **required** output must have been produced (`Ready`); a missing required output is an error (§2.2 "gobpm never silently produces nothing"). An optional output may be absent.
- **G6.** No behaviour change for the existing well-formed examples/tests beyond the new gates: all five examples run; the SRD-007 frame/association/commit path is otherwise untouched.

### 2.2 Non-goals (deferred, each with a named home)

- **`whileExecuting` runtime evaluation** — the `whileExecuting` flag is *stored* (G1) but its mid-execution evaluation (an input pulled / output pushed *during* execution, not at start/completion) is **deferred** to the task-type work that needs it; this SRD's gates only consider non-while-executing parameters.
- **Multiple I/O sets, ordered selection, IORule pairing** — non-goal per ADR-011 v.2 §2.8 (re-adding would mean reintroducing a `Set` abstraction).
- **The service data reader** (ADR-011 §2.6) and the **examples final-pass demo** — the next SRD.
- **Process-level Start/End data special case** (process `DataInput`/`DataOutput`) — lands with the messaging/call-activity work (ADR-011 §2.5).
- The §2.7 **value-notification split** and **event-options unification** — their own SRDs (as in SRD-008 §2.2).

## 3. Requirements

### 3.1 Functional

| # | Requirement |
|---|---|
| FR-1 | `Parameter` (`io_spec_obj.go`) gains `optional bool` and `whileExecuting bool`. `NewParameter` accepts options `data.Optional()` / `data.WhileExecuting()` (default both `false`); accessors `Parameter.IsOptional()` / `Parameter.IsWhileExecuting()`. |
| FR-2 | Remove the `Set` type and its methods (`io_spec_obj.go:99-397`), `SetType`/`allTypes`/`AllSets`/`SingleType`/`CombinedTypes` (`io_spec.go:15-71`), including the dead `Link`/`Unlink`/`linkedSets`. |
| FR-3 | `InputOutputSpecification` (`io_spec.go:86`) drops the `sets map[Direction][]*Set` field and the `AddSet`/`RemoveSet`/`Sets` methods; it keeps `params map[Direction][]*Parameter`, `Parameters(dir)`, `AddParameter`, `RemoveParameter` (the latter loses its per-set removal — there are no sets). It adds `InputSet() []*Parameter` and `OutputSet() []*Parameter` as views over `params[Input]`/`params[Output]`. |
| FR-4 | `InputOutputSpecification.Validate` is rewritten as a **structural** check (no duplicate parameter names per direction; no nil parameter) — the former "param belongs to ≥1 set" / "default params Ready" checks are removed (sets are gone; readiness is a runtime concern, FR-6). |
| FR-5 | Activity construction (`activities/activity_options.go`) replaces `WithSet`/`WithEmptySet`/`setDef`/`addSetParams` with parameter-list options — `WithParameters(dir data.Direction, params ...*data.Parameter)` (each param carries its own `optional`/`whileExecuting`); `WithoutParams()` stays (empty I/O). `createIOSpecs` builds the `IoSpec` from the flagged parameter lists. Events (`events/end_options.go`, `start_options.go`, `event.go`, `end.go`) drop the `inputSet`/`outputSet` `*data.Set` fields and keep their `dataInputs`/`dataOutputs` parameter lists. |
| FR-6 | **Start-gate.** Before an activity/throw-event executes its input associations, a gate checks every **required** (`!optional && !whileExecuting`) input is available — i.e., its filling association can execute (source `Ready`). An unavailable required input → a classified **error** (`errs` class, never a wait, §2.3). An optional input whose association cannot execute is skipped and left `Unavailable`. Wired into `task.LoadData` (`task.go:84`) and `throwEvent.LoadData` (`event.go:363`). |
| FR-7 | **Completion-gate.** After execution, before/at commit, a gate checks every **required** output was produced (`Ready`); a missing required output → a classified **error**. Wired into `task.UploadData` (`task.go:190`). Optional outputs may be absent. |
| FR-8 | The two gates raise **self-identifying** errors (activity/event id + parameter name + direction), per the public-API validation rule. |

### 3.2 Non-functional

| # | Requirement |
|---|---|
| NFR-1 | No behaviour change for valid processes beyond the new gates: data / activities / events / process / instance / thresher tests pass; all five examples run to exit 0. |
| NFR-2 | The SRD-007 path is otherwise untouched — `IoSpec.Parameters(dir)`, `Association.calculate`, `Frame.Commit/Discard` keep their signatures and semantics. |
| NFR-3 | `make ci` green per milestone; diff-coverage ≥95 % (target 100 %) on touched files. |
| NFR-4 | Every new/changed public symbol carries a doc comment; new public API (the param options, `WithParameters`, the gates) validates its inputs with self-identifying errors. |

## 4. Design & implementation plan

### 4.1 Model: parameters carry their role; the IoSpec owns them

```mermaid
flowchart LR
    subgraph after["after (no Set type)"]
        IOS["InputOutputSpecification\nparams[Input], params[Output]"]
        P1["Parameter\nname, ItemAwareElement,\noptional, whileExecuting"]
        IOS -->|InputSet() / OutputSet() views| P1
    end
```

`Set` disappears. `IoSpec.params[Input]` **is** the input set; `params[Output]`
**is** the output set; `InputSet()`/`OutputSet()` are read-only views (BPMN
vocabulary). A `Parameter` carries `optional` (default `false` → required) and
`whileExecuting` (default `false`). `IoSpec.Validate` becomes a structural check.

### 4.2 Construction API

- **Parameter options.** `data.Optional()` and `data.WhileExecuting()` set the
  flags at `NewParameter` time. A required input is the default — you flag the
  exceptions, matching the standard (`optional` default `false`).
- **Activities.** `WithParameters(dir, params...)` adds a direction's parameters
  (each pre-flagged); `WithoutParams()` keeps the empty-I/O case. `WithSet` /
  `WithEmptySet` / `setDef` / `addSetParams` are removed.
- **Events.** `endEvent`/`startEvent` keep their `dataInputs`/`dataOutputs`
  parameter lists and drop the parallel `inputSet`/`outputSet` `*data.Set`.

### 4.3 Runtime gates

- **Start-gate** (`task.LoadData`, `throwEvent.LoadData`): partition the inputs
  into required (`!optional && !whileExecuting`) and the rest. For each required
  input, its filling association must execute (source `Ready`); if it cannot, the
  gate returns a classified error and the frame is discarded — **no wait** (§2.3).
  Optional inputs whose association cannot execute are skipped (left
  `Unavailable`). This makes the today-implicit failure an explicit, optional-aware
  gate.
- **Completion-gate** (`task.UploadData`): after the output associations run, every
  required output (`!optional && !whileExecuting`) must be `Ready`; otherwise a
  classified error (the frame is not committed).
- Both gates reuse the existing association execution and the data-state accessors
  (`ItemAwareElement.State()`), adding only the partition + the
  required-availability/required-production checks.

### 4.4 Milestones (each = one commit, CI-green)

- **M1 — drop `Set`, flag the parameters** (FR-1/2/3/4/5). Atomic across
  `pkg/model/data`, `pkg/model/activities`, `pkg/model/events`,
  `examples/process-data`, and their tests (a type removal cannot be partial and
  stay CI-green). Behaviour-preserving: the runtime still reads
  `IoSpec.Parameters(dir)`; no gate yet. *(If preferred, this can be split
  expand→contract — add the flagged-param API alongside `Set`, migrate, then
  remove `Set` — surfaced at the milestone-plan gate.)*
- **M2 — start-gate** (FR-6/8). Required-input availability check in
  `task.LoadData` + `throwEvent.LoadData`; optional-aware; no-wait error.
- **M3 — completion-gate** (FR-7/8). Required-output production check in
  `task.UploadData`.

### 4.5 Tests (per milestone; details §5)

`io_spec_test.go` / `values_test.go` (no `Set`; `InputSet()`/`OutputSet()` views;
`Validate` structural; param flags + accessors), `activity_options` /
`service_task` tests (`WithParameters`, `WithoutParams`), events tests (param
lists, no `Set`), `task` tests (start-gate: required-missing → error,
optional-missing → ok; completion-gate: required-output-missing → error), and the
five examples as smoke.

## 5. Verification (Definition of Done)

| # | Check | Expectation |
|---|---|---|
| V1 | `data.Set`/`SetType`/`AllSets`/`Link`/`linkedSets` no longer exist; `grep` finds no references repo-wide; packages build (FR-2). | gone |
| V2 | `Parameter` carries `optional`/`whileExecuting` with options + accessors, default required (FR-1). | green |
| V3 | `IoSpec` has no `sets`/`AddSet`/`RemoveSet`/`Sets`; `InputSet()`/`OutputSet()` return the param lists; `Validate` is structural (rejects duplicate names, accepts a well-formed spec) (FR-3/4). | green |
| V4 | Activities build I/O via `WithParameters`/`WithoutParams`; events via param lists; no `*data.Set` field remains (FR-5). | green |
| V5 | Start-gate: a required input whose source is unavailable → classified error, frame discarded, **no wait**; an optional input absent → activity proceeds (FR-6/8). | green |
| V6 | Completion-gate: a required output not produced → classified error, no commit; an optional output absent → commit proceeds (FR-7/8). | green |
| V7 | Regression: data / activities / events / process / instance / thresher pass; all five examples run to exit 0 (NFR-1). | green |
| V8 | `make ci` green; diff-coverage ≥95 % on touched files (NFR-3). | pass |

## 6. Risks & regressions

- **Large atomic M1.** A `Set` removal touches data + activities + events +
  examples + tests in one commit. Mitigated by the runtime reading only
  `IoSpec.Parameters(dir)` (unchanged), so the change is structural; V7 (all
  examples + suites) is the backstop. Expand→contract split available if the
  atomic diff is too large to review.
- **Start-gate over-strict.** Turning the implicit association failure into an
  explicit gate could reject a process that "worked" by accident. Scoped to
  *required* inputs (optional honored); V5 covers both directions, and V7 proves
  the examples still run.
- **Completion-gate breaking empty-output tasks.** Today's common task produces no
  output; an empty output set has no *required* outputs, so the gate is a no-op for
  it. V6/V7 confirm.
- **`whileExecuting` stored but inert.** The flag exists but nothing evaluates it
  yet; the gates explicitly exclude while-executing parameters, so it cannot cause
  a false gate result. Named deferral (§2.2).

## 7. Implementation summary

Landed on branch `feat/io-set-evaluation` (after the ADR-011 v.2 amendment and
the SRD doc) in three milestone commits; `make ci` green and diff-coverage ≥95%
on touched files at each.

### 7.1 Milestones by commit

| Milestone | Commit | Scope | Tests |
|---|---|---|---|
| ADR-011 v.2 | `89ca4fd` | drop-Set conception amendment | — |
| Doc | `57152f9` | SRD-009 | — |
| M1 — drop Set, flag params | `75a8105` | remove `Set`/`SetType`; `Parameter` flags + options; `IoSpec` owns params + `InputSet()`/`OutputSet()`; `WithParameters`; events drop `*data.Set` | `TestParameter`, `TestIOSpec`, `TestIOSpecValidateDuplicateName`, `TestRequiredItemIDs` (+ migrated activity/event tests) |
| M2 — start-gate | `8530583` | `data.RequiredItemIDs`; required-input availability gate in `task.LoadData` / `throwEvent.LoadData` (no wait; optional skipped) | `TestTaskStartGate`, `TestThrowEventStartGate` |
| M3 — completion-gate | `0296ab4` | required-output production gate in `updateOutputs` / `UploadData` (optional absent allowed) | `TestTaskCompletionGate` |

### 7.2 Verification results (§5)

- **V1–V4** — `Set`/`SetType` gone (no repo references); `Parameter` flags +
  accessors; `IoSpec.InputSet()`/`OutputSet()` views; structural `Validate`;
  construction via `WithParameters`/`WithoutParams`; no `*data.Set` field. Green.
- **V5** — start-gate: required input unavailable → classified error, no wait;
  optional input absent → proceeds. Green.
- **V6** — completion-gate: required output not produced → error; optional
  output absent → commit proceeds. Green.
- **V7** — data / activities / events / process / instance / thresher suites
  pass; all five examples run to exit 0.
- **V8** — `make ci` green; diff-coverage M1 96.2% / M2 98.3% / M3 96.9%
  (≥95% on touched files).

### 7.3 Where reality diverged from the §3 draft

- **`ParameterOption` is error-free** (`func(*Parameter)`), not the
  `options.Option` form FR-1's prose implied. The flag options cannot fail, so an
  error return would be an uncoverable branch (the SRD-008 lesson); the simpler
  signature is honest and fully covered.
- **The gates read the IoSpec / dataInputs *definitions*, not the frame
  instances.** `scope.Frame.instantiateParams` rebuilds parameters from name +
  `ItemAwareElement` only, so the per-execution instances do not carry the
  optional/while-executing flags; `data.RequiredItemIDs` therefore runs over the
  definitions (`IoSpec.InputSet()`/`OutputSet()`, `throwEvent.dataInputs`).
- **Availability is state-based, not association-based.** A required input
  pre-seeded `Ready` by its definition passes the start-gate without an
  association; the gate checks the frame instance's state, which is the correct
  BPMN "available" semantics.
- **`throwEvent.LoadData`'s post-check was extracted** to
  `missingRequiredInputs` to keep the function within the gocyclo budget after
  the gate additions.

## 8. References

- [ADR-011 v.2 Process Data Flow](../design/ADR-011-process-data-flow.md) — §2.2
  (one set, per-parameter optional/required/while-executing), §2.3 (availability
  gates the start, never waits), §2.4–§2.5 (associations, events), §2.7 (no `Set`
  type). This SRD lands those; the deferred items (while-executing runtime, the
  service reader, multi-set) are named in §2.2.
- [ADR-010 v.1 Process Data Model](../design/ADR-010-process-data-model.md) — the
  execution frame / association / commit machinery these gates sit on.
- [SRD-007 v.1 Process Data Model](SRD-007-process-data-model.md) — the landing
  that built the frame/association/load-commit path this SRD reuses.
- [SRD-008 v.1 Data model-layer hardening](SRD-008-data-model-hardening.md) — the
  prior data-layer SRD; its single-ownership `Parameter`↔`Set` graph is superseded
  here by removing `Set` (ADR-011 v.2 §2.7).
- [SAD-001 v.1 §14.1](../design/SAD-001-vision-and-architecture.md) — the no-wait
  and single-set deviations this SRD realizes.

## 9. Open questions

- None. The construction-API shape (`WithParameters` + parameter options) and the
  gate placement (`LoadData`/`UploadData`) are decided above; `whileExecuting`
  runtime evaluation, the service reader, and multi-set are deferred with named
  homes (§2.2). Whether M1 lands atomically or expand→contract is a milestone-plan
  detail, not a conception question.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-13 | Ruslan Gabitov | **Accepted**, landed on `feat/io-set-evaluation` (M1 `75a8105`, M2 `8530583`, M3 `0296ab4`, after the ADR-011 v.2 amendment `89ca4fd`); `make ci` green, diff-coverage ≥95% per milestone; all five examples run. §7 filled — see §7.3 for divergences (error-free `ParameterOption`; gates read definitions not frame instances; state-based availability; the `throwEvent` post-check extracted). Lands ADR-011 v.2 §2.2–§2.5 + §2.7 "no `Set` type": drops the reified `Set`/`SetType` (incl. dead `Link`/`linkedSets`), makes required/optional/while-executing per-parameter attributes on `Parameter`, has the `IoSpec` own its parameters directly with `InputSet()`/`OutputSet()` views, replaces the `WithSet`/`WithEmptySet` construction with flagged-parameter options, and adds the runtime start-gate (required inputs available else fail-fast error, no wait; optional may be absent) and completion-gate (required outputs produced else error). `whileExecuting` runtime evaluation, the service reader, and multiple I/O sets are deferred. Three milestones (drop Set → start-gate → completion-gate). Implements ADR-011 v.2; refines ADR-010 v.1. |
