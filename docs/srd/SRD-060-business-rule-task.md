# SRD-060 — Business Rule Task on the pluggable rule-engine seam

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-22 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-027 v.1](../design/ADR-027-business-rule-task-and-rule-engine-seam.md) (the whole conception: the `rules.Engine` seam, decision-by-reference, the batteries-included registry, call/complete/commit semantics) |
| Upstream | [ADR-002 v.2](../design/ADR-002-extension-architecture.md) §4.1/§4.2 (the five-point extension wiring), [ADR-012 v.1](../design/ADR-012-execution-layering.md) (`exec.NodeExecutor`), [SAD-001 v.1](../design/SAD-001-vision-and-architecture.md) N2 |
| Refines | SRD-004 (the extension-skeleton precedent for adding an engine service), SRD-011 (the in-process operation/read-surface idiom this mirrors) — sideways |

Lands ADR-027 v.1: the `rules.Engine` extension point with the in-core
`gorules` decision registry, and the Business Rule Task executing against it.
Closes the BRT half of epic #87 (Script Task stays on the expression-layer
workstream per ADR-027 §2.5).

## §1 Background

- The model stub is bare: `BusinessRuleTask{Implementation string; task}`
  (`pkg/model/activities/brule_task.go`, 11 lines) — no constructor, no
  `Exec`, no `Clone`, no interface asserts, and **no `flow.TaskType` entry**
  (`pkg/model/flow/activity.go:28-39` lists Receive/Script/Send/Service/
  User/Manual only). Conformance row 6: "🟡 model-only … needs a rule-engine
  extension seam (the ADR-002 pattern)".
- The seam template is `expression.Engine`
  (`pkg/model/expression/expression.go:21-24`) — one `Evaluate` method; and
  the five-point wiring is fully paved: `thresherConfig` field
  (`pkg/thresher/options.go:28`), default in `defaultConfig()`
  (`options.go:326` `goexpr.New()`), nil-guarded option
  (`WithExpressionEngine`, `options.go:160`), `EngineRuntime` accessor
  (`pkg/renv/engineruntime.go:30`), startup printout
  (`pkg/thresher/thresher.go:305`).
- The execution precedent is `ServiceTask.Exec`
  (`service_task.go:221-270`): invoke → wrap the returned
  `*data.ItemDefinition` as a Ready `data.Parameter` → `re.Put` →
  `selectOutgoing`; and `gooper.OpFunctor` (`gooper.go:29-33`) is the
  read-surface-in / item-out functor shape the registry's decisions mirror.
- The house pattern for a new task type is `ManualTask`
  (`manual_task.go`): constructor via `newTask`, `Node/Clone/TaskType/Exec`,
  the three interface asserts.

## §2 Requirements

### §2.1 Functional

- **FR-1 — the `rules` package.** New `pkg/rules`:
  - `rules.Engine` — the seam (ADR-027 §2.1): `Type() string` (the
    `##`-kind) and `Evaluate(ctx, decisionRef string,
    r service.DataReader) (*data.ItemDefinition, error)`.
  - `rules.DecisionFunc` — the registered-decision shape:
    `func(ctx, r service.DataReader) (*data.ItemDefinition, error)` (the
    `gooper.OpFunctor` idiom minus the input message).
- **FR-2 — the in-core default: `pkg/rules/gorules`.** A bounded decision
  registry implementing `rules.Engine` (`Type() = "##GoRules"`):
  `New(...)` + `Register(name string, d DecisionFunc) error` validating a
  non-empty name, a non-nil functor, and rejecting duplicates; `Evaluate` of
  an unregistered name is a **classified error** (fail loud — ADR-027 §2.4);
  registration-only growth (bounded by construction). A `MustRegister`
  panic-twin for fixture building.
- **FR-3 — five-point wiring.** `thresherConfig` gains `ruleEngine
  rules.Engine`; `defaultConfig()` sets `gorules.New()`; a nil-guarded
  `WithRuleEngine(e rules.Engine)` option; `renv.EngineRuntime` gains
  `RuleEngine() rules.Engine` (+ the `thresherConfig` accessor);
  `logStartupConfig` gains the `module("ruleEngine", …)` line. Mocks
  regenerated (`make gen_mock_files` — the widened `EngineRuntime` flows
  into `mockrenv`).
- **FR-4 — the Business Rule Task model.** `flow.BusinessRuleTask TaskType`
  enum constant; the stub reshaped to the house pattern: unexported
  `decisionRef string` (the dead exported `Implementation` field **removed**
  — pre-1.0, never constructible; per ADR-027 §2.2 the implementation kind
  is execution-time information), `NewBusinessRuleTask(name, decisionRef
  string, opts ...options.Option)` validating both non-empty,
  `DecisionRef()` getter, `Node()/Clone()/TaskType()`, and the three
  interface asserts (`flow.Node`, `flow.Task`, `exec.NodeExecutor`).
- **FR-5 — execution (ADR-027 §2.3).** `Exec(ctx, re)`: nil-guard `re`;
  `out, err := re.RuleEngine().Evaluate(ctx, brt.decisionRef, re)` (the
  `RuntimeEnvironment` satisfies `service.DataReader` structurally — the
  ServiceTask precedent); an error returns as the task's failure (the
  ordinary fault path — a typed `BpmnError` from a decision travels the
  Error machinery unchanged); a non-nil result wraps as a Ready
  `data.Parameter` under the item's ID and commits via `re.Put` (the
  `service_task.go:250-267` shape — the result-variable mapping); finish
  with `selectOutgoing(ctx, re)` (conditional/default flow rules apply).

### §2.2 Non-functional

- **NFR-1 — validate-all-params** on every new public surface (constructor,
  `Register`, `WithRuleEngine`), self-identifying messages.
- **NFR-2 — no new runtime state**: the task is a plain activity; the seam
  adds no loop or instance state.
- **NFR-3 — coverage**: `make ci` green; diff-coverage ≥95% (aim 100%);
  touched functions ≥80%.

## §3 Models (shapes)

```go
// pkg/rules/rules.go (FR-1)
type Engine interface {
	// Type names the engine kind for the task's implementation attribute
	// and the startup printout ("##GoRules", "##DMN", …).
	Type() string
	// Evaluate resolves decisionRef and evaluates it against the read-only
	// process-data surface, returning one structured result item.
	Evaluate(ctx context.Context, decisionRef string,
		r service.DataReader) (*data.ItemDefinition, error)
}

type DecisionFunc func(ctx context.Context,
	r service.DataReader) (*data.ItemDefinition, error)

// pkg/rules/gorules/gorules.go (FR-2)
func New() *Registry
func (r *Registry) Register(name string, d rules.DecisionFunc) error
func (r *Registry) MustRegister(name string, d rules.DecisionFunc) *Registry

// pkg/model/activities/brule_task.go (FR-4)
type BusinessRuleTask struct {
	decisionRef string
	task
}
func NewBusinessRuleTask(name, decisionRef string,
	opts ...options.Option) (*BusinessRuleTask, error)
```

Wiring points (FR-3, verified): `options.go:28` (config struct), `:326`
(defaults), `:160` (the option idiom), `engineruntime.go:30` (accessor row),
`thresher.go:305` (printout row).

## §4 Analysis & decisions

- **§4.1 The engine reads through the task's own environment.** `Evaluate`
  receives the executing `RuntimeEnvironment` as its `service.DataReader` —
  the decision sees exactly what an in-process Go operation sees
  (frame-first, then the scope walk-up), so data visibility rules are
  inherited, not reinvented. *Alternative — an explicit input-mapping layer:*
  rejected for the first landing; the ioSpec/data-association machinery the
  activity already carries covers declared I/O.
- **§4.2 Result naming = the item's ID.** The decision returns an
  `*data.ItemDefinition` whose ID is the variable name (the service-task
  result idiom — `service_task.go:254` builds the parameter from
  `out.ID()`); a nil result with a nil error is legal (a decision with
  side conclusions only — nothing commits). *Alternative — a `resultVariable`
  attribute on the task:* deferred; the item-ID convention is the engine-wide
  norm and keeps the seam one-method.
- **§4.3 The dead `Implementation` field goes.** The stub's exported field
  was never constructible or read; ADR-027 §2.2 makes the implementation
  kind execution-time information (`re.RuleEngine().Type()`), reported in
  the node's observability details rather than stored on the model.

## §6 Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | `gorules` unit (`pkg/rules/gorules`) | FR-2: register/evaluate roundtrip; empty name, nil functor, duplicate rejected; unknown ref → classified error; `Type()` |
| T-2 | task model (`pkg/model/activities`) | FR-4: constructor validation (empty name/ref), `DecisionRef()`, `TaskType()`, `Clone` carries the ref, asserts compile |
| T-3 | `Exec` against mock renv (`pkg/model/activities`) | FR-5: `Evaluate` called with the ref; result `Put` as a Ready parameter; a nil result commits nothing; an engine error fails the Exec |
| T-4 | wiring (`pkg/thresher`) | FR-3: the default engine is `gorules`; `WithRuleEngine` overrides; `WithRuleEngine(nil)` rejected |
| T-5 | e2e (`pkg/thresher`) | full path: a BRT evaluates a registered decision, the result routes an XOR downstream; instance completes |
| T-6 | fault path (`pkg/thresher` or instance) | FR-5: a decision returning `BpmnError` is caught by an Error boundary on the BRT |
| T-7 | example (`examples/business-rule-task/`) | smoked exit 0; the decision result visibly drives the flow |

## §7 Milestones

- **M1 — the seam + the default.** FR-1, FR-2; T-1.
  `feat(rules): the rule-engine seam and the gorules registry`.
- **M2 — the task + wiring.** FR-3, FR-4, FR-5; mocks regen; T-2…T-4.
  `feat(activities): Business Rule Task on the rule-engine seam`.
- **M3 — e2e + example + doc sync.** T-5…T-7; `examples/business-rule-task/`
  (+ index row), CHANGELOG, conformance row 6 → ✅, README(+ru), roadmap,
  issue #87 checkbox tick at handover.
  `feat: Business Rule Task — e2e, example, doc sync`.

## §8 Cross-doc

- Implements **ADR-027 v.1** (whole conception).
- Upstream: **ADR-002 v.2** §4.1/§4.2, **ADR-012 v.1**, **SAD-001 v.1** N2.
- Sideways: **SRD-004** (extension skeleton), **SRD-011** (in-process
  operation idiom).
- Part of **#87** (the BRT half; Script Task remains, riding #74 per
  ADR-027 §2.5).

## §9 Definition of Done

- [ ] FR-1…FR-5 implemented; every §6 test exists and passes.
- [ ] `make ci` green; diff-coverage ≥95% (aim 100%); touched functions ≥80%.
- [ ] The default engine works zero-config (T-4) and an unknown decision
      fails loud (T-1).
- [ ] `examples/business-rule-task/` runs exit 0; binary gitignored; index row.
- [ ] §10 filled; conformance row 6 → landed; README(+ru)/roadmap synced;
      ADR-027 status flip at landing.

## §10 Implementation summary

*Filled at landing.*

## Open questions

*None — §4 resolves the design points inline.*

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-22 | Ruslan Gabitov | Initial draft — lands ADR-027 v.1: `pkg/rules` (`Engine` + `DecisionFunc`), the bounded `gorules` registry default (`##GoRules`, fail-loud unknown refs), the five-point thresher/renv wiring (`WithRuleEngine`, `RuleEngine()` accessor, startup printout), and the Business Rule Task rebuilt to the house pattern (enum entry, `NewBusinessRuleTask(name, decisionRef)`, Exec = evaluate → Put → selectOutgoing; the dead exported `Implementation` field removed). Three milestones. |
