# SRD-066 — The expression-engine registry and the text-expression kind

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-25 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-032 v.1](../design/ADR-032-language-routed-expression-engines.md) §2.1/§2.2 + §2.4/§2.5 (the widened engine contract, the core language registry with the batteries default and the `WithoutDefaultExpressionEngines` opt-out, the text-expression kind with the `Body()` capability, no per-evaluation facts; §2.3 — the `gobpm:lite` evaluator — rides SRD-067) |
| Upstream | [ADR-031 v.1](../design/ADR-031-script-task-and-script-engine-seam.md) (the registry mechanics this mirrors), [ADR-002 v.2](../design/ADR-002-extension-architecture.md) §4.1/§4.2 |
| Refines | SRD-064 v.1 (the script-registry landing pattern: repeatable option, build-at-New conflicts, startup routing lines) — sideways |

Lands ADR-032's routing half: the widened `expression.Engine`, the
language `Registry` (itself an `Engine` — every runtime consumer is
untouched), the repeatable/opt-out wiring, and the text `FormalExpression`
kind. The lite evaluator, the example and the #74 closure ride SRD-067.

## §1 Background

- The seam today (`pkg/model/expression/expression.go`): one method —
  `Evaluate(ctx, expr, src)`; the default (`expression/goexpr.Engine`)
  delegates to the expression's own `Evaluate`; `WithExpressionEngine`
  replaces wholesale (`pkg/thresher/options.go`), the enginert builder
  likewise (`internal/enginert/enginert.go`).
- Ten-plus runtime consumers reach the engine only through
  `EngineRuntime.ExpressionEngine()` (conditions/activation, timers, MI,
  std-loop, correlation, conditional events, `DeriveKey`) — the accessor's
  return type stays `expression.Engine`, so **a Registry implementing
  `Engine` leaves every consumer untouched**, including the worker
  dispatcher's optional `tasks.ExpressionEngineBinder`
  (`pkg/tasks/workerdispatcher.go:195` — the bound value is the registry).
- The functor kind (`data/goexpr.GExpression`) carries
  `language = "gobpm:goexpr"` (`pkg/model/data/goexpr/goexpr.go:18`) —
  the claim the widened `goexpr` engine takes.
- The registry mechanics are landed and proven in `pkg/script` (SRD-064):
  enumerable claims, case-normalized map, loud duplicate/unclaimed
  errors, build-at-`New`, startup routing lines.
- `mockrenv` mocks `EngineRuntime` (return types unchanged — no interface
  change lands there); no generated mock exists for `expression.Engine`
  itself.

## §2 Requirements

### §2.1 Functional

- **FR-1 — the widened contract.** `expression.Engine` gains `Type()
  string` (the `##`-kind) and `Languages() []string` (the enumerable
  claims); `Evaluate` unchanged. `expression/goexpr.Engine` implements
  them: `Type() = "##GoExpr"`, `Languages() = {"gobpm:goexpr"}` — the
  delegate semantics unchanged.
- **FR-2 — the Registry.** `expression.Registry`, itself an `Engine`
  (mirroring `script.Registry`): `NewRegistry(engines ...Engine)` — nil
  engine, claimless engine, blank claim and duplicate claim (naming both
  kinds and the language) reject construction; claims normalize
  case-insensitively; immutable after construction. `Type()` = `"##None"`
  empty / joined kinds otherwise; `Languages()` = the sorted claims;
  `Evaluate` validates (`expr` non-nil; an **empty `Language()`** is a
  classified error per ADR-032 §2.1), routes by the expression's
  language, and fails an unclaimed language **listing the registered
  claims** (the empty registry names `WithExpressionEngine` in its
  message).
- **FR-3 — the wiring.** `thresherConfig` gains the engine accumulator +
  the built registry + the `noDefaultExprEngines` flag;
  **`WithExpressionEngine(e)` becomes repeatable** (nil rejected;
  appends); **`WithoutDefaultExpressionEngines()`** suppresses the
  batteries default; `thresher.New` builds the registry once — defaults
  (`goexpr.New()`; SRD-067 adds `lite`) prepended unless suppressed —
  **claim conflicts fail `New` loud**; the accessor returns the registry;
  `logStartupConfig` prints the `expressionEngine` module line, the
  aggregated kinds and one routing line per language.
  `internal/enginert`: the default becomes `registry(goexpr)`; the
  `WithExpressionEngine(e)` builder is **replaced by
  `WithExpressionRegistry(reg)`** (nil ignored; `NewRegistry` stays the
  single conflict authority) — its two in-repo test consumers adjust.
- **FR-4 — the text kind.** `data.NewTextExpression(language, body
  string, opts ...options.Option) (*TextExpression, error)` — both
  trimmed-non-empty (fail-fast; the metamodel's 0..1 is the interchange
  affordance); `WithResultType(rt string)` option (the
  `evaluatesToTypeRef` analog; default empty). Implements
  `FormalExpression`: `Language()`/`ResultType()` from construction;
  `Evaluate` and `Result` fail loud ("a text expression evaluates through
  the engine registry"); `IsEvaluated()` = false. Implements the new
  **`data.BodyHolder`** capability (`Body() string`) — the one-method
  interface engines assert to reach the source; `GExpression` does not
  implement it.
- **FR-5 — compatibility proof.** The functor path is regression-locked:
  a zero-config engine evaluates existing `goexpr` conditions unchanged
  (the whole existing suite is the proof — no consumer file changes), and
  the dispatcher binder receives the registry as its `expression.Engine`.

### §2.2 Non-functional

- **NFR-1 — validate-all-params**; no `Must*` calls in library paths (the
  muststyle guard covers the touched packages).
- **NFR-2 — no per-evaluation facts** (ADR-032 §2.4) — the diff adds no
  Reporter calls on the evaluation path.
- **NFR-3 — coverage**: `make ci` green; diff-coverage ≥95% (aim 100%);
  touched functions ≥80%.

## §3 Models (shapes)

```go
// pkg/model/expression (FR-1/FR-2)
type Engine interface {
	Type() string
	Languages() []string
	Evaluate(ctx context.Context, expr data.FormalExpression,
		src data.Source) (data.Value, error)
}

func NewRegistry(engines ...Engine) (*Registry, error)
func (reg *Registry) Type() string        // "##None" | "##GoExpr+##Lite"
func (reg *Registry) Languages() []string // sorted claims
func (reg *Registry) Evaluate(...)        // routes by expr.Language()

// pkg/model/expression/goexpr (FR-1)
// Type() = "##GoExpr"; Languages() = {"gobpm:goexpr"}; Evaluate delegates.

// pkg/model/data (FR-4)
type BodyHolder interface {
	Body() string
}

func NewTextExpression(language, body string,
	opts ...options.Option) (*TextExpression, error)
```

Wiring points (FR-3, all landed and re-verified this session): the
config/option/accessor blocks in `pkg/thresher/options.go`, the registry
build + startup lines in `pkg/thresher/thresher.go` (the SRD-064 shape),
the builder/accessor pair in `internal/enginert/enginert.go`.

## §4 Analysis & decisions

- **§4.1 The Registry is an `Engine`** — the accessor's type doesn't
  change, so all ten-plus consumers and the dispatcher binder compile and
  behave untouched; only the wiring layer knows about registration.
- **§4.2 Conflicts surface in `thresher.New`** (the options accumulate;
  `NewRegistry` runs once where an error can return) — the SRD-064
  pattern verbatim; `enginert`'s builder-style takes a pre-built registry
  for the same reason `WithScriptRegistry` does.
- **§4.3 The defaults prepend, user engines append.** A user engine
  claiming `gobpm:goexpr` therefore conflicts loudly rather than silently
  shadowing the battery — overriding a battery requires
  `WithoutDefaultExpressionEngines()` plus explicit registration, an
  intentional two-step.
- **§4.4 `BodyHolder` is a capability, not a contract change.** Existing
  `FormalExpression` implementors (the functor kind, test fakes) stay
  valid; engines that interpret text assert the capability and fail loud
  on an expression that lacks it.

## §6 Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | registry unit (`pkg/model/expression`) | FR-2: two-engine routing (case-insensitive); duplicate-claim rejection naming both kinds; nil/claimless/blank rejections; `##None` + the `WithExpressionEngine` hint; unclaimed language lists claims; empty `Language()` rejected; `Type()` aggregation |
| T-2 | goexpr widening (`pkg/model/expression/goexpr`) | FR-1: `##GoExpr`, the `gobpm:goexpr` claim, delegation unchanged |
| T-3 | text kind (`pkg/model/data`) | FR-4: constructor validation (empty language/body), `WithResultType`, `Language`/`Body`/`ResultType` getters, `Evaluate`/`Result` fail loud, `IsEvaluated` false, the `BodyHolder` assert — and `GExpression` does NOT implement `BodyHolder` |
| T-4 | wiring (`pkg/thresher` + `internal/enginert`) | FR-3: zero-config = `##GoExpr` registry (startup line + routing line); repeatable registration of a fake second engine; claim conflict fails `New` naming both kinds; `WithExpressionEngine(nil)` rejected; `WithoutDefaultExpressionEngines()` → `##None` and a functor condition faults loud with the claims listed; enginert default/`WithExpressionRegistry` |
| T-5 | e2e (`pkg/thresher`) | multi-engine live: a fake `x-test:expr` engine registered beside the default; one process mixes a functor condition and a text-expression condition (`NewTextExpression("x-test:expr", ...)`) — each routes to its engine and the flow follows both results; the full existing suite green = the FR-5 regression lock |

## §7 Milestones

- **M1 — the contract, the registry, the text kind.** FR-1/FR-2/FR-4;
  T-1…T-3.
  `feat(expression): the language registry and the text-expression kind (SRD-066 M1)`.
- **M2 — wiring + e2e + seam doc sync.** FR-3/FR-5; T-4/T-5; CHANGELOG
  (the routing half); the roadmap's WS-E `adapters/feel` note gains "the
  seam now hosts engines side by side".
  `feat(thresher): language-routed expression wiring (SRD-066 M2)`.

## §8 Cross-doc

- Implements **ADR-032 v.1** §2.1/§2.2/§2.4/§2.5.
- Upstream: **ADR-031 v.1**, **ADR-002 v.2**.
- Sideways: **SRD-064 v.1** (the mirrored mechanics).
- Part of **#74** (closed by SRD-067's landing).

## §9 Definition of Done

- [ ] FR-1…FR-5 implemented; every §6 test exists and passes.
- [ ] `make ci` green; diff-coverage ≥95% (aim 100%); touched functions
      ≥80%.
- [ ] The zero-config functor path is regression-locked (the full suite,
      unchanged consumer files) and the opt-out faults loud (T-4).
- [ ] §10 filled; CHANGELOG synced.

## §10 Implementation summary

*Filled at landing.*

## Open questions

*None — §4 resolves the design points inline.*

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-25 | Ruslan Gabitov | Initial draft — lands ADR-032's routing half: the widened `expression.Engine` (`Type`/`Languages`), the language `Registry` (an `Engine` itself — all consumers untouched incl. the dispatcher binder; loud conflicts/unclaimed/empty-language errors with claims listed), the repeatable `WithExpressionEngine` + `WithoutDefaultExpressionEngines` wiring with build-at-`New` conflicts and startup routing lines, `enginert.WithExpressionRegistry`, and the text `FormalExpression` kind (`NewTextExpression(language, body)`, the `BodyHolder` capability, loud self-evaluation refusal). Two milestones; the lite evaluator, example and #74 closure ride SRD-067. |
