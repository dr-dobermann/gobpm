# SRD-069 — Rules/Script invocation facts and the registrar audit

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-07-26 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-013 v.2](../design/ADR-013-instance-observability.md) §2.6 (the open phase taxonomy — two new phases, one reused; the masking and volume rules govern every addition) |
| Upstream | [ADR-027 v.1](../design/ADR-027-business-rule-task-and-rule-engine-seam.md) (the rules seam whose calls and registrars this audits), [ADR-031 v.1](../design/ADR-031-script-task-and-script-engine-seam.md) (the script seam), [ADR-029 v.1](../design/ADR-029-decision-table-engine-adapter.md) §2.6 (the deploy surface) |
| Refines | SRD-060 v.1 (the `KindRules` outcome pair), SRD-064 v.1 (the `KindScript` outcome pair) — sideways |

Closes two observability gaps found reviewing the Business Rule / Script
task facts (the post-ADR-032 review the owner requested): (1) both kinds
are **outcome-only** — no invocation-phase fact, so engine-call latency
and a hung engine are invisible; (2) the **commit-stage blind spot** — a
successful engine call whose result *commit* fails faults the task with
no closing `KindRules`/`KindScript` fact at all. It also extends the
audit to the **registrar surfaces** (register/deploy) the engines expose
outside instance execution.

## §1 Background

- **The outcome pairs today.** `KindRules`: `Evaluated`
  (`brule_task.go:136` — decision_ref, implementation, row_count,
  result_variable) / `Failed` on an evaluation error
  (`brule_task.go:118`; Warn echo, `echolevel.go:57`). `KindScript`:
  `Executed` (`script_task.go:149`) / `Failed` on an execution error
  (`script_task.go:132`; Warn echo). Both emit **only after the engine
  call returns**.
- **The commit blind spot.** `commitResult` (`brule_task.go:168`) and
  `commitOutput` (`script_task.go:173`) failures return straight to the
  fault machinery — no `KindRules`/`KindScript` fact marks them; the
  decision/script audit stream goes silent mid-story.
- **`KindNodeProgress`** (`Entered`/`Executing`, Debug) brackets every
  node but cannot distinguish "inside the engine call" from anything
  else the node does.
- **The registrar surfaces are invisible.** `gorules.Registry.Register`
  (`gorules.go:46`), `dtable.Engine.Register` (`engine.go:53`) and
  `dtable.Engine.Deploy` (`deploy.go:44` — decode, then **replace**
  `e.tables[t.name]` under the mutex) run outside instance execution,
  called by user code; the adapters hold no Reporter. A runtime `Deploy`
  that replaces a live decision emits nothing today. No
  remove/unregister surface exists in-repo (grep) — no `Removed` phase
  is invented.
- **The binding precedent.** `tasks.ReporterBinder`
  (`workerdispatcher.go:204`) is the optional capability the engine
  binds its sink into at startup: `thresher.New` does
  `ob.BindReporter(t.producer)` (`thresher.go:257-258`); a dispatcher
  that doesn't implement it simply doesn't emit. `enginert.Runtime`
  carries its own reporter (`enginert.go:50`).
- **The e2e hook**: `Thresher.Observe(o) *Subscription`
  (`producer.go:153`) subscribes an engine-wide observer;
  `dtable` (its own module, `replace ../..`) can import `thresher` in
  its tests, while core tests cannot import adapters — the e2e split
  follows the module boundary.
- `AttrDecisionRef`/`AttrImplementation`/`AttrError` exist
  (`fact.go:162-167`); no `stage`/`rule_count` attrs yet;
  `PhaseRegistered` exists (`fact.go:56`), `Invoked`/`Deployed` do not.

## §2 Requirements

### §2.1 Functional

- **FR-1 — the invocation phase.** New reused **`PhaseInvoked`**
  (`// Rules / Script`): emitted immediately **before** the engine call
  — `BusinessRuleTask.Exec` before `eng.Evaluate` (details:
  decision_ref, implementation) and `ScriptTask.Exec` before
  `eng.Execute` (details: script_format, implementation via
  `routedKind`). One extra fact per task execution — bounded and
  per-task, not the per-expression flood ADR-032 §2.4 forbids. Paired
  with the closing fact it makes engine-call latency derivable and a
  hung engine attributable.
- **FR-2 — close the pair.** Every `Invoked` closes with
  `Evaluated`/`Executed` or `Failed`. Commit-stage failures
  (`commitResult`/`commitOutput`) now also emit `Failed`, distinguished
  by the new **`AttrStage = "stage"`** detail: `engine` on an engine
  failure, `commit` on a commit failure. Success facts are unchanged
  (no stage attr).
- **FR-3 — the engine Reporter seam.** New optional capability
  **`rules.ReporterBinder`** (the `tasks.ReporterBinder` shape:
  `BindReporter(sink observability.Reporter)`). `thresher.New` binds
  `t.producer` into `cfg.RuleEngine()` when implemented (beside the
  dispatcher binds, `thresher.go:257`); `enginert` binds its own
  reporter likewise. An engine that doesn't implement it doesn't emit —
  and an implementing engine emits **nothing while unbound** (the
  pre-`New` construction wiring is code, not runtime governance; the
  audit targets what changes after the engine is live).
- **FR-4 — gorules registration facts.** `gorules.Registry` implements
  `rules.ReporterBinder` (sink guarded by the existing mutex);
  `Register` emits **`KindRules`/`PhaseRegistered`** after success —
  details: decision_ref, implementation (`##GoRules`). Failed
  registrations return their error to the caller and emit nothing (the
  audit records the catalog, not caller mistakes). `MustRegister` rides
  `Register`.
- **FR-5 — dtable registration/deploy facts.** `dtable.Engine`
  implements the binder; `Register` emits `Registered` (decision_ref,
  implementation `##DTable`, **`AttrRuleCount = "rule_count"`**);
  `Deploy` emits new **`PhaseDeployed`** — details: decision_ref,
  implementation, rule_count, `replaced` (`"true"` when the name
  overwrote a live table, else `"false"`). Emission after the swap,
  outside the lock's hot section where practical.
- **FR-6 — echo posture.** `{KindRules, PhaseDeployed}` overrides to
  **Info** (a runtime governance milestone — the `ProcessLifecycle`
  analog); `Registered` and `Invoked` ride the kind default (Debug).

### §2.2 Non-functional

- **NFR-1 — masking**: every new detail is a name, kind, count or flag —
  never a rule body, script source, or payload value.
- **NFR-2 — volume**: no per-evaluation/per-rule/per-row facts; engine
  **internals stay out of the observer stream** (their future home is
  the Tracer seam, not facts) — this SRD adds at most one fact per task
  execution and one per registrar call.
- **NFR-3 — no `Must*` in library paths; validate-all-params** (a nil
  sink in `BindReporter` is ignored, keeping the current no-op).
- **NFR-4 — coverage**: `make ci` green; diff-coverage ≥95% (aim 100%);
  touched functions ≥80%; the dtable module's own suite green.

## §3 Models (shapes)

```go
// pkg/observability/fact.go
PhaseInvoked  Phase = "Invoked"  // Rules / Script — the engine call opens
PhaseDeployed Phase = "Deployed" // Rules — a definition landed at runtime

AttrStage     = "stage"      // engine | commit (Failed only)
AttrRuleCount = "rule_count" // registrar facts (dtable)

// pkg/rules (FR-3) — the tasks.ReporterBinder shape
type ReporterBinder interface {
	BindReporter(sink observability.Reporter)
}
```

Worked trace (the audit stream for one BRT execution against a deployed
table): `engine.Deploy(tableJSON)` at runtime →
`Rules/Deployed{decision_ref: discount, implementation: ##DTable,
rule_count: 3, replaced: true}` at **Info**; the instance reaches the
task → `Rules/Invoked{decision_ref: discount, implementation:
##DTable}` → the call returns rows and the fold commits →
`Rules/Evaluated{…, row_count: 1, result_variable: discount}`. Had the
commit failed instead: `Rules/Failed{…, stage: commit, error: …}` —
the pair still closes.

## §4 Analysis & decisions

- **§4.1 One reused `Invoked`, not "Called"/"Loaded".** The taxonomy
  reuses phases across kinds (`Completed`, `Failed`, `Registered`);
  two kind-specific synonyms would say nothing extra.
- **§4.2 The binder over a constructor option.** The engines are built
  by the user before `thresher.New`, so only the engine (not the core)
  can emit registrar facts — but the sink is the core's. The
  established answer is the optional-capability bind at `New`
  (`tasks.ReporterBinder` precedent); a `WithReporter` constructor
  option would demand the user hold a reporter before the engine
  exists.
- **§4.3 Unbound = silent, deliberately.** Pre-`New` registrations are
  construction-time wiring — auditable as code; the governance-relevant
  events are the ones that change a **live** engine (runtime `Deploy`),
  and those happen bound. No buffering/replay machinery for pre-bind
  facts.
- **§4.4 Registrar failures emit nothing.** The error returns to the
  caller synchronously — the caller is present and told; the audit
  records the resulting catalog. (Task-side `Failed` differs: the
  "caller" is a process instance and the stream is its audit.)
- **§4.5 Internals stay out.** Per-rule condition hits, hit-policy row
  matching, script VM activity are per-evaluation × per-rule — the
  flood ADR-013's volume rules exist to prevent; their diagnostic
  content already rides the `Failed` error details (dtable errors name
  decision/rule/datum). Deep latency insight belongs to the Tracer.

## §6 Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | BRT facts (`pkg/model/activities`) | FR-1/FR-2: the `Invoked→Evaluated` pair with details; engine failure → `Invoked` + `Failed{stage: engine}`; commit failure (Put rejected) → `Invoked` + `Failed{stage: commit}` |
| T-2 | ScriptTask facts (`pkg/model/activities`) | FR-1/FR-2: the same trio over the script pair (`Executed`/`Failed`) |
| T-3 | gorules registrar (`pkg/rules/gorules`) | FR-3/FR-4: bound → `Registered` with details; unbound → no fact, no panic; a rejected duplicate emits nothing; nil sink ignored |
| T-4 | dtable registrar (`adapters/dtable`) | FR-5: `Register` → `Registered` with rule_count; `Deploy` fresh → `Deployed{replaced: false}`, redeploy → `{replaced: true}`; unbound silent; plus the module-side e2e — deploy against a live thresher and see `Deployed` at the engine observer |
| T-5 | wiring + e2e (`pkg/thresher`) | FR-3/FR-6: `New` binds the producer into a fake `ReporterBinder` rules engine; a BRT process run shows `Invoked→Evaluated` on the `Observe` stream; the `{KindRules, Deployed}` Info override in the echo table |

## §7 Milestones

- **M1 — task invocation facts.** FR-1/FR-2 (+ the two attrs and
  `PhaseInvoked`); T-1/T-2.
  `feat(observability): Rules/Script invocation facts close the pair (SRD-069 M1)`.
- **M2 — the registrar audit.** FR-3…FR-6 (+ `PhaseDeployed`); T-3…T-5;
  CHANGELOG.
  `feat(rules): the engine Reporter seam and registrar facts (SRD-069 M2)`.

## §8 Cross-doc

- Implements **ADR-013 v.2** §2.6 (open phases; masking/volume rules).
- Upstream: **ADR-027 v.1**, **ADR-029 v.1** §2.6, **ADR-031 v.1**.
- Sideways: **SRD-060 v.1**, **SRD-064 v.1** (the outcome pairs this
  completes).

## §9 Definition of Done

- [x] FR-1…FR-6 implemented; every §6 test exists and passes.
- [x] `make ci` green (core) + the dtable module suite green;
      diff-coverage ≥95% (aim 100%); touched functions ≥80%.
- [x] Masking/volume audit: no new detail carries a body/source/value;
      no per-evaluation fact added.
- [x] §10 filled; CHANGELOG synced.

## §10 Implementation summary

Landed on `feat/rules-script-observability` in two milestones, as
planned:

| Milestone | Commit | Scope |
|---|---|---|
| M1 | `a1f9336` | FR-1/FR-2: `PhaseInvoked` + `AttrStage`, both task `Exec` paths open and close the pair (commit failures included); T-1/T-2 |
| M2 | `93f99ef` | FR-3…FR-6: `rules.ReporterBinder` + the `thresher.New`/`enginert` binds, gorules/dtable `Registered`, dtable `Deployed{replaced}` at Info, `AttrRuleCount`; T-3…T-5; CHANGELOG |

- **Verification**: post-M2 `make ci` exit 0; diff-coverage **100.0% of
  55 changed lines**; the dtable module suite green with every touched
  function at 100%; the decision-table example smokes exit 0 (it
  deploys pre-`New` — unbound, silent, the §4.3 posture demonstrated).
- **Deltas vs the draft**: none — the plan landed as written. One
  emphasis note: gorules emits under its registration write-lock
  (Report is contractually non-blocking, `fact.go` Reporter doc);
  dtable emits inside the deploy critical section for the same reason —
  the `replaced` probe and the emission stay atomic with the swap.

## Open questions

*None — §4 resolves the design points inline.*

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-26 | Ruslan Gabitov | Initial draft — the post-ADR-032 observability review's remediation: the reused `Invoked` phase opens the Rules/Script audit pair (engine-call latency derivable, hung engines attributable), commit-stage failures close it (`stage: engine｜commit` — the blind spot where a successful call's failed commit left the stream silent), and the registrar surfaces gain their audit through the `rules.ReporterBinder` capability (the dispatcher-binder precedent): `Registered` for gorules/dtable registration, `Deployed` at Info for runtime dtable deploys (`replaced` flagged). Unbound engines stay silent by design; engine internals stay out of the stream (Tracer territory). |
