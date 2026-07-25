# SRD-064 — The Script Engine seam (multi-engine) and the Script Task

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-25 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-031 v.1](../design/ADR-031-script-task-and-script-engine-seam.md) §2.1–§2.3 + §2.5 (the seam, the registry/router, the empty-registry default, the task semantics, the observability mirror; §2.4 — the Lua adapter — rides SRD-065) |
| Upstream | [ADR-027 v.1](../design/ADR-027-business-rule-task-and-rule-engine-seam.md) §2.5 (the fulfilled promise), [ADR-002 v.2](../design/ADR-002-extension-architecture.md) §4.1/§4.2, [ADR-012 v.1](../design/ADR-012-execution-layering.md) |
| Refines | SRD-060 v.1 (the rule-engine seam landing this mirrors: five-point wiring, task Exec shape, per-engine facts) — sideways |

Lands ADR-031's core half: `pkg/script` (the Engine contract + the
multi-engine format Registry), the five-point thresher/renv wiring with
repeatable registration, the ScriptTask rebuilt to the house pattern with
per-name output commits, and the `Script` observability kind. The Lua
interpreter is SRD-064's; this SRD proves everything with test engines.

## §1 Background

- The model stub is bare (`pkg/model/activities/script_task.go`:
  `ScriptTask{ScriptFormat, Script string; task}` — exported dead fields,
  no constructor/Exec/asserts, the exact SRD-060 §1 starting shape); the
  enum entry **exists** (`pkg/model/flow/activity.go`: `ScriptTask
  TaskType = "ScriptTask"`).
- The five-point wiring template is landed twice (expression, rules):
  `thresherConfig` + `defaultConfig()` + option + `EngineRuntime` accessor
  (implementors: `pkg/thresher/options.go` `thresherConfig`,
  `internal/enginert/enginert.go` `Runtime`) + `logStartupConfig`
  `module(...)` lines (`pkg/thresher/thresher.go`), with `make
  gen_mock_files` regenerating `mockrenv`.
- The per-engine observability mirror is landed (`KindRules`,
  SRD-060 FR-6): kind + phases + `Attr*` keys + echo levels; node-side
  facts route through the instance emission point (the `execEnv.Reporter`
  override).
- The commit helpers are landed (FIX-026): `data.ReadyValueParameter`
  builds a Ready datum from a name + `data.Value` — the per-name output
  commit is one call per output.
- Conformance row 5 (`ScriptTask` execution, 🟡 model-only, #87) flips at
  SRD-065 when the batteries interpreter and the example land — this SRD
  makes the row *implementable by registration*.

## §2 Requirements

### §2.1 Functional

- **FR-1 — `pkg/script`: the contract and the Registry.**
  - `script.Outputs` — the named results: `map[string]data.Value`.
  - `script.Engine` — `Type() string` (the `##`-kind),
    `Formats() []string` (the enumerable MIME claims, non-empty for a
    real engine), `Execute(ctx, format, script string,
    r service.DataReader) (Outputs, error)`.
  - `script.Registry` — the core router, itself a `script.Engine`:
    `NewRegistry(engines ...Engine)` folds claims into a
    format→engine map, normalizing case (`strings.ToLower`, trimmed);
    a nil engine, an engine with no formats, an empty format, or a
    **duplicate claim** (error names both engine kinds and the format)
    reject construction. `Type()` = `"##None"` when empty, else the
    joined kinds (`"##Lua+##Starlark"`); `Formats()` = the sorted claim
    list; `Execute` routes by the normalized format — an unclaimed
    format is a classified error **listing the registered formats**
    (empty registry: the wire-an-adapter message, ADR-031 §2.2).
- **FR-2 — five-point wiring, registry-shaped.** `thresherConfig` gains
  `scriptEngines []script.Engine`; **`WithScriptEngine(e)` is repeatable**
  (nil rejected; each call appends); `thresher.New` builds the registry
  once via `script.NewRegistry` — **claim conflicts surface as New
  errors** (construction-time, loud); the built registry lands in the
  config; `renv.EngineRuntime` gains `ScriptEngine() script.Engine`
  (never nil — the empty registry default); `internal/enginert.Runtime`
  gains a `WithScriptRegistry(reg)` builder (nil ignored, its default the
  empty registry) — conflicts always surface through `NewRegistry`, which
  the caller runs; `logStartupConfig` prints the `scriptEngine` module
  line (the registry's aggregated kind) plus **one routing line per
  format** (`scriptFormat text/x-lua: ##Lua`). Mocks regenerated.
- **FR-3 — the ScriptTask model.** The stub rebuilt to the house pattern:
  unexported `scriptFormat`, `script` fields;
  `NewScriptTask(name, format, script string, opts ...options.Option)` —
  all three trimmed-non-empty (an engine choice: the metamodel carries
  both as 0..1, but a programmatic model with a scriptless ScriptTask is
  a modeling error — fail fast); `ScriptFormat()`/`Script()` getters;
  `Node()/Clone()/TaskType()` (the enum entry exists) and the three
  interface asserts.
- **FR-4 — execution (ADR-031 §2.3).** `Exec(ctx, re)`: nil-guard `re`;
  `outs, err := re.ScriptEngine().Execute(ctx, st.scriptFormat,
  st.script, re)`; an error reports the `Script/Failed` fact and returns
  (the ordinary fault path — routing misses included); a non-empty result
  commits **per name in sorted order** (deterministic — map iteration is
  not) via `data.ReadyValueParameter` + `re.Put`, wrapped with the task
  identity on failure; then the `Script/Executed` fact;
  `selectOutgoing(ctx, re)`.
- **FR-5 — observability.** `KindScript` with `PhaseExecuted` (new) and
  the reused `PhaseFailed`; details: `AttrScriptFormat` (new),
  `AttrImplementation` (the answering engine's kind — for `Executed`, the
  routed engine; for a routing miss, the registry's aggregate),
  `AttrOutputCount` (new; names and counts only — the masking rule).
  Echo: `KindScript` at Debug, `{KindScript, PhaseFailed}` at Warn.

### §2.2 Non-functional

- **NFR-1 — validate-all-params**; no `Must*` calls in library paths
  (the muststyle guard applies — `pkg/script` sits under `pkg/`).
- **NFR-2 — stdlib-light**: `pkg/script` imports core packages only.
- **NFR-3 — coverage**: `make ci` green; diff-coverage ≥95% (aim 100%);
  touched functions ≥80%.

## §3 Models (shapes)

```go
// pkg/script/script.go (FR-1)
type Outputs map[string]data.Value

type Engine interface {
	Type() string
	Formats() []string
	Execute(ctx context.Context, format, script string,
		r service.DataReader) (Outputs, error)
}

// pkg/script/registry.go (FR-1)
func NewRegistry(engines ...Engine) (*Registry, error)
func (reg *Registry) Type() string       // "##None" | "##Lua+##Starlark"
func (reg *Registry) Formats() []string  // sorted claims
func (reg *Registry) Execute(...)        // routes; unclaimed = loud
func (reg *Registry) EngineFor(format string) (Engine, bool) // the
// routed engine, for the task's Executed-fact implementation detail

// pkg/model/activities/script_task.go (FR-3)
type ScriptTask struct {
	scriptFormat string
	script       string
	task
}
func NewScriptTask(name, format, script string,
	opts ...options.Option) (*ScriptTask, error)
```

Wiring points (FR-2, all previously landed and re-verified): the config
struct/defaults/option block in `pkg/thresher/options.go`, the accessor
block in `pkg/renv/engineruntime.go`, the builder/accessor pair in
`internal/enginert/enginert.go`, the `module(...)` block in
`pkg/thresher/thresher.go`.

## §4 Analysis & decisions

- **§4.1 The registry is an `Engine`.** The task talks to one interface;
  composition is invisible to it. `EngineFor` exists solely so the
  `Executed` fact can name the *routed* engine's kind rather than the
  aggregate (FR-5) — the task calls `Execute` for the work itself.
- **§4.2 Repeatable option, build-at-New.** Options accumulate; the
  conflict check runs once in `thresher.New` where an error can surface
  (options.go's error-style idiom). `enginert`'s builder-style can't
  error, so it takes a pre-built registry — `NewRegistry` remains the
  single conflict authority.
- **§4.3 Sorted-name commits.** Go map iteration would make commit order
  (and any commit failure) non-deterministic across runs; sorting output
  names makes failures reproducible. No semantic ordering is implied —
  each output is an independent datum.
- **§4.4 Required format/script at construction.** The metamodel's 0..1
  is an interchange affordance; programmatically a scriptless ScriptTask
  is a bug — fail-fast at build (the decisionRef precedent, SRD-060
  §4.3-adjacent). Recorded as an engine note in ADR-031 §2.5's spirit.

## §6 Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | registry unit (`pkg/script`) | FR-1: two-engine routing (case-insensitive formats); duplicate-claim rejection naming both kinds; nil/formatless engine rejected; empty registry `##None` + the wire-an-adapter error; unclaimed format lists registered formats; `Type()` aggregation; `EngineFor` |
| T-2 | task model (`pkg/model/activities`) | FR-3: constructor validation (empty name/format/script), getters, `TaskType()`, `Clone` carries format+script |
| T-3 | `Exec` vs mock renv + stub engine | FR-4/FR-5: `Execute` called with format+script; outputs committed per-name in sorted order (a multi-output script; the Put matcher sees the slice); empty outputs commit nothing; engine error → `Script/Failed` fact + task failure; the `Executed` fact carries format/kind/count |
| T-4 | wiring (`pkg/thresher` + `internal/enginert`) | FR-2: default is the empty registry (`##None` in the startup log + routing lines absent); repeatable `WithScriptEngine` registers two engines (routing lines printed); a claim conflict fails `thresher.New`; `WithScriptEngine(nil)` rejected; enginert default/`WithScriptRegistry` |
| T-5 | e2e (`pkg/thresher`, test engines) | the full path: two stub engines registered, a ScriptTask routes to the right one by format, its outputs are read downstream; an unclaimed format faults the instance loud; the empty-registry default faults loud |

## §7 Milestones

- **M1 — `pkg/script`.** FR-1; T-1.
  `feat(script): the Script Engine contract and the multi-engine format registry`.
- **M2 — the task + the wiring + the facts.** FR-2…FR-5; mocks regen;
  T-2…T-4.
  `feat(activities): Script Task on the Script Engine seam`.
- **M3 — e2e + partial doc sync.** T-5; CHANGELOG (seam half); the
  conformance row 5 note updated to "seam landed; interpreter rides
  SRD-065" (the flip itself, README/tour and the example ride SRD-065).
  `feat: Script Task — e2e and seam doc sync`.

## §8 Cross-doc

- Implements **ADR-031 v.1** §2.1–§2.3/§2.5.
- Upstream: **ADR-027 v.1** §2.5, **ADR-002 v.2**, **ADR-012 v.1**.
- Sideways: **SRD-060 v.1** (the mirrored landing).
- Part of **#87** (Script Task — the issue's second half; with SRD-065 it
  closes) and the **#74** boundary note: the expression seam is untouched.

## §9 Definition of Done

- [ ] FR-1…FR-5 implemented; every §6 test exists and passes.
- [ ] `make ci` green; diff-coverage ≥95% (aim 100%); touched functions
      ≥80%.
- [ ] The empty-registry default fails loud (T-5) and a claim conflict
      fails `thresher.New` (T-4).
- [ ] §10 filled; CHANGELOG synced; conformance row 5 note updated.

## §10 Implementation summary

*Filled at landing.*

## Open questions

*None — §4 resolves the design points inline.*

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-25 | Ruslan Gabitov | Initial draft — lands ADR-031's core half: `pkg/script` (`Engine` with enumerable `Formats()`, `Outputs`, the `Registry` router — loud claim conflicts, `##None` empty default, unclaimed-format errors listing the claims), the registry-shaped five-point wiring (repeatable `WithScriptEngine`, conflicts surfacing in `thresher.New`, per-format startup routing lines, `enginert.WithScriptRegistry`), the ScriptTask rebuilt (required name/format/script, per-name sorted output commits via `ReadyValueParameter`), and `KindScript` facts (`Executed`/`Failed`, format/kind/count details). Three milestones; the Lua interpreter, the example, and the row-5 flip ride SRD-065. |
