# SRD-004 — Extension Skeleton (minimal/default implementation of ADR-002)

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-08 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-002 v.1 Extension Architecture](../design/ADR-002-extension-architecture.md) |
| Refines | [SAD-001 v.1 §11 Extension Model](../design/SAD-001-vision-and-architecture.md) |

This SRD lands the **foundational extension skeleton** of [ADR-002](../design/ADR-002-extension-architecture.md): every extension contract defined in `pkg/`, each with a **bundled default** (default behaviour only — no production adapters), the functional-options assembly on `thresher.New`, the **`EngineRuntime`/`RuntimeEnvironment` split**, and the startup log line — wired so a zero-option engine runs today's BPMN end-to-end. Completing this SRD closes ADR-002's §7 acceptance gate (the defaults-only rows) and flips ADR-002 → Accepted.

## 1. Background & motivation

### 1.1 Current state (verified against the code)

- **Engine constructor has no injection point** — `func New(id string) (*Thresher, error)` (`pkg/thresher/thresher.go:116`); single-arg, no options.
- **`RuntimeEnvironment` is internal with four methods** — `internal/renv/renv.go:12`: embeds `scope.Scope`; `InstanceID() string`; `EventProducer() eventproc.EventProducer`; `RenderRegistrator() interactor.Registrator`.
- **`Instance` already implements it** — `internal/instance/instance.go:821` (`var _ renv.RuntimeEnvironment = (*Instance)(nil)`), with `InstanceID()` / `EventProducer()` / `RenderRegistrator()` methods (`instance.go:803/808/813`).
- **`EventHub` is internal** — `internal/eventproc/eventproc.go:47` (`EventHub`, `EventProducer`, `EventProcessor`); not externally implementable.
- **Human interaction is internal** — `internal/interactor` (`Registrator`, `Renderer` ecosystem); reached via `RenderRegistrator()`.
- **Expressions are a model type** — `pkg/model/data/expression.go:72` (`FormalExpression`); evaluated directly, not behind an engine-level interface.
- **No infrastructure extension points** — `Repository`, `Logger`, `Tracer`, `MetricsRecorder`, `Clock`, `MessageBroker`, `AuthorizationProvider`, `WorkerDispatcher` do not exist anywhere (per ADR-002 §1).

### 1.2 Why

ADR-002 (reconciled to ADR-001 v.3) is the agreed extension architecture, but none of it is built. Per ADR-002 §5/§7, the departures land **together, at minimal/default behaviour, in this one foundational SRD**; ADR-002 flips to Accepted when this SRD's §7 tests pass. Production adapters and per-interface depth are later, separately-gated SRDs.

## 2. Goals & scope

### 2.1 Goals (in scope)

- **G1.** Define all 11 extension contracts in `pkg/` (per ADR-002 §4.2): `Logger` (slog-satisfiable core interface — `*slog.Logger` implements it directly), OTel-shaped `Tracer`/`MetricsRecorder` (modeled on the OTel API but **core does not import OTel** — SAD-001 G2), `Clock`, `Repository`, `MessageBroker`, `ExpressionEngine`, `AuthorizationProvider`, `WorkerDispatcher` interfaces; `EventHub` interface relocated to `pkg/` (impl stays `internal/`); `TaskDistributor` promoted from `internal/interactor.Registrator` (renamed).
- **G2.** Ship a **bundled default** in core for each: `slog.Default()` Logger · **no-op `Tracer`** (with an in-memory recent-spans ring available as an opt-in) · **in-memory queryable, series-capped `MetricsRecorder`** (`Snapshot()` for tests/diagnostics) · wall-clock `Clock` · in-memory non-durable `Repository` · in-memory inbox `MessageBroker` · Go-native `ExpressionEngine` (wrapping the existing evaluator) · allow-all `AuthorizationProvider` · in-process `WorkerDispatcher` · current EventHub impl · current interactor impl.
- **G3.** **Functional-options assembly** — `thresher.New(id string, opts ...Option) (*Thresher, error)`; `defaultConfig()` wires all defaults; each `WithXxx` overrides one; last-write semantics; **no `NewDefault`**. Zero-option `New(id)` produces a working engine.
- **G4.** **Startup log line** — one INFO `thresher.starting` record listing the resolved wiring (per ADR-002 §4.4.1).
- **G5.** **Factor `EngineRuntime` + extend `RuntimeEnvironment`** (ADR-002 §4.3): define the engine/server-level `EngineRuntime` interface (the resolved services) implemented by `Thresher`; move `internal/renv` → `pkg/renv`; `RuntimeEnvironment` **embeds `EngineRuntime`** + instance-local; rename `RenderRegistrator()` → `TaskDistributor()`; `Instance` **embeds** the Thresher's `EngineRuntime` (engine methods promoted — no per-method delegates) and adds its instance-local methods; track call sites stay uniform (`t.inst.X()`).
- **G6.** **Wire into current execution** the extensions today's BPMN actually exercises: route `FormalExpression` evaluation through `ExpressionEngine`; source time from `Clock` (timer handling) instead of `time.Now`; use the configured `EventHub`/`Logger`. Engine runs the current element set unchanged.
- **G7.** **No external behaviour change** for implemented elements (None Start/End, Service/User tasks, Exclusive gateway, sequence flows incl. conditions/default, timer events): existing tests + `examples/*` pass unchanged; `make ci` green.

### 2.2 Non-goals (explicitly deferred)

- **N1.** **Production adapters** (`adapters/*` — postgres, otel, casbin, FEEL, redis/nats brokers, …) → later SRDs per ADR-002 §4.6.
- **N2.** **Execution hook-sites for the not-yet-exercised services.** `Repository`, `MessageBroker`, `AuthorizationProvider`, `WorkerDispatcher` are **defined + defaulted + accessible** via the engine and `RuntimeEnvironment`, but their call-sites are **not** wired in this SRD — no current BPMN feature needs them yet:
  - `Repository` checkpoint/load/rehydrate call-sites → the Persistence & State ADR (ADR-001 v.3 §4.7).
  - `MessageBroker` correlation routing → the message-correlation SRD.
  - `AuthorizationProvider` enforcement at sensitive ops → an authz SRD.
  - `WorkerDispatcher` remote dispatch → the distribution SRD (per SAD-001 §13.2).
  The skeleton makes them present and overridable; *invoking* them is out of scope.
- **N3.** **Final `pkg/` package paths** — ADR-003 owns the exact layout. This SRD places interfaces per ADR-003's proposed per-concern layout; paths are **provisional** until ADR-003 lands (a later move is a mechanical rename).
- **N4.** **The `RuntimeAware` adapter-injection hook** (ADR-002 §3.5 Pattern C / §8.3) — the skeleton **defines** `EngineRuntime` and `RuntimeEnvironment`, but has no adapters to inject into, so the `UseRuntime(EngineRuntime)` hook and its assembly-time wiring land with the first real adapter.
- **N5.** **Adapter contract-test helpers, optional side-capability interfaces (`Starter`/`Stopper`/…), cluster awareness** (ADR-002 §8.3/§8.4) — land with the first real adapter.

## 3. Requirements

### 3.1 Functional

| # | Requirement | Acceptance |
|---|---|---|
| FR-1 | The 11 contracts exist in `pkg/` (G1). `EventHub` interface relocated (impl stays internal); `TaskDistributor` = renamed promoted `Registrator`. | `grep` finds each interface under `pkg/`; build passes; `internal/` impls import the `pkg/` interfaces. |
| FR-2 | Each contract has a bundled core default (G2). | A default value exists and satisfies its interface; constructed by `defaultConfig()`. |
| FR-3 | `thresher.New(id, opts ...Option)`; zero-option works; `WithXxx` overrides; last-write; no `NewDefault`. | `New("x")` runs; `WithLogger(a),WithLogger(b)` ⇒ b; no `NewDefault` symbol. |
| FR-4 | One INFO `thresher.starting` log line lists every resolved engine extension by impl type. | Capture-logger test: exactly one record, key `thresher.starting`, attrs per ADR-002 §4.4.1. |
| FR-5 | `EngineRuntime` (engine services) defined in `pkg/renv`, implemented by `Thresher`; `RuntimeEnvironment` **embeds** it + instance-local; `RenderRegistrator()`→`TaskDistributor()`; `Instance` **embeds** the Thresher's `EngineRuntime`. | `var _ renv.EngineRuntime = (*Thresher)(nil)` and `var _ renv.RuntimeEnvironment = (*Instance)(nil)`. |
| FR-6 | `Instance` embeds the Thresher's `EngineRuntime` (engine methods promoted, no per-method delegates); track reaches services via its one `*Instance`. | Per-method test: `instance.Logger()` (etc.) == the engine's configured value. |
| FR-7 | `ExpressionEngine`, `Clock`, `EventHub`, `Logger` are wired into current execution (G6): `FormalExpression` evaluated via `ExpressionEngine`; timer time via `Clock`. | Override-and-observe tests: a custom `ExpressionEngine`/`Clock` is the one used during execution. |
| FR-8 | `Repository`/`MessageBroker`/`AuthorizationProvider`/`WorkerDispatcher` are defined, defaulted, and reachable via the engine/RE, but **not** invoked by execution (N2). | They're constructable and accessible (`instance.Repository()` etc.); no execution call-site references them yet. |
| FR-9 | No regression for implemented elements (G7). | Existing `internal/instance` + engine tests and `examples/*` pass unchanged; `make ci` green. |

### 3.2 Non-functional

| # | Requirement | Acceptance |
|---|---|---|
| NFR-1 | Race-free under the detector. | `make ci` (race-gated) green. |
| NFR-2 | Touched/created files meet the coverage standard (≥80%, aim 100%; gated by `covercheck`). | `make cover-check` PASS on the diff. |
| NFR-3 | `core` gains no non-stdlib runtime dependency (SAD-001 G2). Upheld even for telemetry: `Tracer`/`MetricsRecorder` are OTel-*shaped* but core does **not** import OTel (ADR-002 §4.2); the real OTel types live in `adapters/otel/`. | `go mod graph` shows no new external core dep (defaults are stdlib-only; `slog` is stdlib; no `go.opentelemetry.io/*` in core). |
| NFR-4 | Visible-by-default observability preserved (Logger default `slog.Default()`). | Zero-option engine logs to the default handler. |

## 4. Design & implementation plan

### 4.1 Shapes (illustrative; exact `pkg/` paths per ADR-003)

```go
// Engine-level config holds the resolved extensions (one per interface).
type thresherConfig struct {
    logger      Logger          // slog-satisfiable interface; default slog.Default()
    tracer      Tracer          // OTel-shaped, core-defined (no OTel import); default no-op
    metrics     MetricsRecorder // default = in-memory queryable, series-capped registry
    clock       Clock
    repository  Repository
    msgBroker   MessageBroker
    exprEngine  ExpressionEngine
    authz       AuthorizationProvider
    dispatcher  WorkerDispatcher
    eventHub    eventproc.EventHub
    taskDist    TaskDistributor
}

type Option func(*thresherConfig)

func WithLogger(l Logger) Option { return func(c *thresherConfig) { c.logger = l } }  // *slog.Logger satisfies Logger
// … one per extension …

func New(id string, opts ...Option) (*Thresher, error) {
    cfg := defaultConfig()          // all defaults wired
    for _, o := range opts { o(&cfg) }
    t, err := assemble(id, cfg)
    if err != nil { return nil, err }
    t.logStartupConfig()            // §4.4.1
    return t, nil
}
```

Per ADR-002 §4.3 the engine services are factored into an **`EngineRuntime`** interface that `Thresher` implements (returning the resolved `thresherConfig` values); `RuntimeEnvironment` (moved to `pkg/renv`) **embeds `EngineRuntime`** + instance-local methods; `Instance` **embeds** the Thresher's `EngineRuntime` (engine methods promoted — no per-method delegates) and keeps its instance-local methods. Track call sites are unchanged in style (`t.inst.Clock().Now()`).

### 4.2 Milestones (each independently buildable + CI-green)

1. **M1 — Observability + Clock.** `Logger` (slog-satisfiable interface), OTel-shaped `Tracer`/`MetricsRecorder` (no OTel import), `Clock` interfaces + defaults: `slog.Default()` Logger, **no-op Tracer**, **in-memory queryable series-capped Metrics registry**, wall-clock Clock — plus an opt-in in-memory recent-spans ring Tracer for dev/tests. Pure leaf packages; no engine wiring yet. Default/conformance tests (incl. the registry `Snapshot()` + series-cap behaviour).
2. **M2 — Stateful leaves.** `Repository`, `MessageBroker`, `AuthorizationProvider`, `WorkerDispatcher` interfaces + defaults (in-mem / in-mem inbox / allow-all / in-proc). Defined + tested; not yet invoked (N2).
3. **M3 — Promotions.** `ExpressionEngine` (interface wrapping the `FormalExpression` evaluator) + default; `EventHub` interface → `pkg/eventproc` (impl stays internal, imports redirected); `TaskDistributor` (promote `interactor.Registrator`, rename).
4. **M4 — Assembly.** `thresherConfig` + `Option` + `WithXxx` (one per extension) + `New(id, opts...)` refactor + `defaultConfig()` + `logStartupConfig()` (§4.4.1). No `NewDefault`.
5. **M5 — EngineRuntime + RuntimeEnvironment.** Move `internal/renv` → `pkg/renv`; define `EngineRuntime` (engine services) implemented by `Thresher`; `RuntimeEnvironment` embeds it; rename `RenderRegistrator()`→`TaskDistributor()`; `Instance` embeds the Thresher's `EngineRuntime`; redirect imports; **wire ExpressionEngine + Clock into execution** (G6).
6. **M6 — Acceptance.** ADR-002 §7 applicable suite (zero-option New e2e, options compose/override/last-write, startup log, Instance-implements-RE, delegates, default conformance, RE composition) + examples pass + `make ci` green + `cover-check` PASS. Flip **ADR-002 + SRD-004 → Accepted** + RU twins.

Sequencing: leaves (M1/M2) and promotions (M3) define the contracts+defaults with no engine coupling; assembly (M4) wires them into `New`; RE extension (M5) exposes them through `Instance` and wires the executed ones; M6 verifies + accepts.

## 5. Verification (Definition of Done)

Maps to [ADR-002 §7](../design/ADR-002-extension-architecture.md) (defaults-only rows; adapter-dependent rows deferred to the first adapter SRD):

| Test | Asserts |
|---|---|
| Zero-option New e2e | `thresher.New("t")` registers + runs a process to completion with all defaults. |
| Options compose / override / last-write | all `WithXxx` in random order ⇒ same state; each overrides its default; last write wins. |
| Startup config log | exactly one INFO `thresher.starting` with an attr per engine extension = wired impl type. |
| Thresher implements EngineRuntime; Instance implements RuntimeEnvironment | `var _ renv.EngineRuntime = (*Thresher)(nil)` and `var _ renv.RuntimeEnvironment = (*Instance)(nil)`. |
| Engine-service delegates | `instance.X()` == engine config's X, per method. |
| Default conformance | the in-memory `Repository` default (and others) pass a default-behaviour test. |
| Executed-extension wiring | a custom `ExpressionEngine`/`Clock` is the one used during execution (FR-7). |
| No regression | existing suite + `examples/*` pass unchanged; `make ci` green; `cover-check` PASS. |

**DoD:** all FR/NFR satisfied; the table above green; `make ci` + `cover-check` green; ADR-002 §7 (applicable rows) satisfied. On that, flip ADR-002 + SRD-004 → Accepted + RU twins.

## 6. Risks & regressions

- **RuntimeEnvironment move (`internal/renv`→`pkg/renv`) ripples imports.** Mitigation: M5 is its own milestone; redirect all importers; `make ci` gates.
- **EventHub interface relocation may touch many import sites.** Mitigation: M3 splits interface from impl, keeps the impl in `internal/`, redirects imports only.
- **ExpressionEngine indirection changes evaluation path.** Mitigation: default wraps the *existing* evaluator; FR-7 override test + no-regression suite (G7).
- **Scope creep into N2 hook-sites.** Repository/MessageBroker/AuthZ/WorkerDispatcher are define-and-default only; resist wiring call-sites here.
- **Provisional package paths (ADR-003).** Mitigation: paths are per ADR-003's proposed layout; a later move is mechanical.
- **`core` dependency creep.** Mitigation: all defaults stdlib-only; NFR-3 `go mod graph` check.

## 7. Implementation summary

> ⚠️ TODO: filled at landing — milestone SHAs, files/lines, deviations, V-results.

## 8. References

- [ADR-002 v.1 Extension Architecture](../design/ADR-002-extension-architecture.md) — §3.5 cross-adapter composition (EngineRuntime / Pattern C), §4.2 catalogue, §4.3 EngineRuntime + RuntimeEnvironment, §4.4 assembly, §4.5 defaults, §5 departures, §7 acceptance gate (this SRD closes the defaults-only rows), §8.3 `RuntimeAware`.
- [ADR-001 v.3 Execution Model](../design/ADR-001-execution-model.md) — §4.7 runtime invariants (Repository target); §4.3 event stream (EventHub/Logger consumers).
- [ADR-003 v.1 Module Layout](../design/ADR-003-module-layout.md) — final `pkg/` paths (provisional here).
- [SAD-001 v.1 §11 Extension Model](../design/SAD-001-vision-and-architecture.md); §9.2 multi-module; G2 minimal-core-deps.
- Existing code: `pkg/thresher/thresher.go:116`; `internal/renv/renv.go:12`; `internal/eventproc/eventproc.go:47`; `internal/interactor/`; `pkg/model/data/expression.go:72`; `internal/instance/instance.go:803-821`.

## 9. Open questions

1. **Observability package grouping** — one `pkg/observability` (Logger/Tracer/MetricsRecorder) vs separate `pkg/logger`,`pkg/tracer`,`pkg/metrics`? ADR-003 §3.2 prefers "one subpackage per cohesive concern". **Resolved provisionally:** group as `pkg/observability` for the cohesive sinks; final call rides ADR-003. Confirm at M1.
2. **`MetricsRecorder` placement?** **Resolved:** `MetricsRecorder()` is a method on `EngineRuntime` (ADR-002 §4.3); since `RuntimeEnvironment` embeds `EngineRuntime`, it is reachable both engine-side (`Thresher`) and instance-side (`Instance`) with no special-casing.
3. **`ExpressionEngine` interface shape** — minimal `Evaluate(expr data.FormalExpression, scope scope.Scope) (any, error)`? **Resolved provisionally:** mirror the current evaluator's call signature so the default is a thin wrapper; pin exact signature at M3 from the call-sites.
4. **Default telemetry implementation** — no-op vs visible vs log-backed? **Resolved (design discussion):** defaults differ by signal cost (ADR-002 §4.2). **Metrics** default to an in-memory, series-capped, queryable registry (visible by default per the observability policy; `Snapshot()` makes tests trivial — no logtel needed). **Tracing** defaults to no-op (a span is a per-event allocation, inert without a backend) with an in-memory recent-spans ring as a one-line opt-in. A persistent SQL telemetry sink is a future production adapter (`adapters/sqlstore`), never a core default. The earlier logging-backed telemetry idea is **dropped** (it turns metrics into log text that must be parsed back).

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-08 | Ruslan Gabitov | Initial Draft. Foundational extension skeleton for ADR-002 — 11 contracts in `pkg/` + bundled defaults, functional-options assembly, `EngineRuntime`/`RuntimeEnvironment` split (Thresher implements `EngineRuntime`; `RuntimeEnvironment` embeds it; `Instance` embeds it), startup log; executed extensions (ExpressionEngine/Clock/EventHub/Logger) wired, the rest define-and-default only (N2); `RuntimeAware` adapter-injection deferred (N4). Closes ADR-002 §7 defaults-only rows on landing. |
