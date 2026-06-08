# ADR-002 — Extension Architecture

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-05-30 |
| Owner | Ruslan Gabitov |
| Supersedes | — |
| Refines | [SAD-001 v.1 §11 Extension Model](SAD-001-vision-and-architecture.md) |

## 1. Context

SAD-001 §11 named an extension catalogue and said "Go-idiomatic: interfaces + functional options." This ADR locks the interface set, the assembly pattern, the public/internal split, the default-implementation policy, and the adapter-module conventions.

The current code already has a partial extension surface, mostly in `internal/`:

| Existing interface | Location | Role |
|---|---|---|
| `EventHub`, `EventProducer`, `EventProcessor`, `EventWaiter` | `internal/eventproc/eventproc.go` | Event distribution model |
| `Scope`, `NodeDataLoader`, `NodeDataConsumer`, `NodeDataProducer` | `internal/scope/scope.go` | Hierarchical data scoping |
| `RuntimeEnvironment` (composite of `Scope + InstanceID + EventProducer + RenderRegistrator`) | `internal/renv/renv.go` | Per-instance context bag |
| `Interactor`, `Registrator`, `RenderController` | `internal/interactor/interactor.go` | Human interaction abstraction |
| `FormalExpression`, `Source`, `PropertyAdder` | `pkg/model/data/` | BPMN expression / data integration |

Engine façade:

- `pkg/thresher/thresher.go` exposes `Thresher.New(id string)` — single-arg constructor; **no way to inject extensions at construction**.
- `Thresher` runs the instance registry + event registration but has no Repository, Logger, Tracer, Clock, or any other infrastructure-injection point.

**Missing extension points** (per SAD-001 §11): `Repository`, `Logger`, `Tracer`, `MetricsRecorder`, `Clock`, `MessageBroker`, `AuthorizationProvider`, `WorkerDispatcher`. None of these have an interface anywhere yet.

The decision below establishes the catalogue, the public/internal split, the assembly pattern, and how the existing extension surface evolves to fit.

## 2. Decision

**Two-layer extension model. Engine-level extensions are registered once at `Thresher` construction via functional options; Instance-level context (the existing `RuntimeEnvironment`) is composed per-instance from engine-level extensions plus instance-scoped concerns. All extension interfaces live in `pkg/`; default implementations ship in core; production implementations live in `adapters/*` modules per SAD-001 §9.2.**

Summary table:

| Concern                     | Mechanism                                                                                                                                                                |
| --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Extension catalog ownership | This ADR defines the full set. Future additions amend this ADR (version bump).                                                                                           |
| Public surface              | Every replaceable interface lives in `pkg/`; exact subpackage layout deferred to ADR-003.                                                                                |
| Internal-only surface       | Implementation glue (e.g., `EventWaiter`, `NodeDataLoader`) stays in `internal/`.                                                                                        |
| Assembly                    | `thresher.New(id string, opts ...thresher.Option) (*Thresher, error)` + functional options. Zero-option call `thresher.New("id")` produces a working engine — every option simply overrides a default. **No separate `NewDefault` constructor**; defaults are the default. |
| Per-instance composition    | The existing `RuntimeEnvironment` interface — which Instance already implements — is **extended** with the new engine-level service methods. Track gets one external reference at construction: its owning `*Instance`. Track call sites are uniform: `t.inst.Scope().GetData(...)` for instance-local state and `t.inst.Logger().Info(...)` for engine services. Instance-local fields are owned directly by Instance; engine-level methods on Instance are one-line delegates to engine config. The runtime values are owned by Engine, exposed via Instance through composition. |
| Cross-adapter composition   | Adapters do NOT depend on each other's packages. By default an adapter **shares the engine's resources via the injected `EngineRuntime`** (the optional `RuntimeAware` hook — §3.5 Pattern C / §8.3): e.g. AuthZ uses `rt.Repository()` unless explicitly given its own. The user MAY **split** by passing a per-adapter option. See §3.5 / §4.6. |
| Default-impl policy         | Engine-level defaults ship in core, visible-by-default (Logger = `slog.Default()` per project policy); production swaps via `WithXxx` options pulling from `adapters/*`. |
| Stability contract          | Each public extension interface is a semver-stable contract once Accepted. Breaking changes → new ADR + version bump.                                                    |

## 3. Alternatives Considered

### 3.1 Plugin loading mechanism

| Option | Description | Verdict |
|---|---|---|
| **Go `plugin` package (.so files)** | Engine dlopen()s shared objects at runtime; users compile their plugin separately. | Rejected. Mac/Windows support is partial; version compatibility is fragile; conflicts with Go's static-binary value proposition; not idiomatic. |
| **Generic interfaces + compile-time wiring** — chosen | Users implement Go interfaces; pass impls to engine constructor; standard `go build` produces one binary. | Selected. Native Go pattern; works on all platforms; debuggability and observability are normal Go semantics. |
| **gRPC sidecar plugins (Hashicorp-style)** | Plugins are separate processes communicated with via gRPC. | Rejected for v.1. Heavyweight; introduces IPC failure modes for what is fundamentally an in-process library. Re-evaluable for the runtime layer's distribution story (per [SAD-001 §13](SAD-001-vision-and-architecture.md)) but not for core. |

### 3.2 Assembly pattern

| Option | Description | Verdict |
|---|---|---|
| **Builder pattern** (`thresher.NewBuilder().WithLogger(l).Build()`) | Fluent builder API | Rejected. More ceremony; harder to add options without breaking the builder method set; less Go-idiomatic. |
| **Config struct** (`thresher.New(cfg Config)`) | Single config struct holding everything | Rejected. Forces users to construct (and zero-init or partially populate) a fat struct; hides which fields are required vs optional; merging defaults is awkward. |
| **Functional options** — chosen | `thresher.New(id, WithLogger(l), WithRepository(r), ...)` | Selected. Idiomatic Go; trivial to add new options without API break; each option is self-documenting; defaults are explicit. |
| **Method chaining post-construction** (`thresher.New(...).WithLogger(l)`) | Mutating builder methods after construction | Rejected. Engine state is fragile during mutation; race risk; allows partially-configured engines to start. |

### 3.3 Public / internal split for existing interfaces

| Option | Description | Verdict |
|---|---|---|
| **Keep all extensions in `internal/`** | External users can't implement them; only intra-project use | Rejected. Defeats SAD-001's embeddable + extensible vision. |
| **Move ALL existing interfaces to `pkg/`** | Wholesale move | Rejected. Some (`EventWaiter`, `NodeDataLoader`) are implementation glue tightly bound to engine internals; exposing them as stability contracts is over-commitment. |
| **Selectively expose what's a real extension point** — chosen | Promote interfaces that users genuinely replace; keep impl glue private | Selected. Per §4 below. |

### 3.4 Default implementation locality

| Option | Description | Verdict |
|---|---|---|
| **All defaults in core; `New` applies them, options override** — chosen | `thresher.New(id)` with no options produces a working engine with all defaults wired; each `WithXxx(...)` overrides one default | Selected. Out-of-the-box use is the primary path; adapters are pull-when-needed. No separate `NewDefault` function — `New` IS the default. |
| **No defaults; user must wire everything** | Core ships interfaces only; every extension explicit | Rejected. Worst-case 10-extension wiring for a `Hello World` example. Breaks SAD-001's out-of-the-box usability goal. |
| **Defaults in a `gobpm-defaults` module** | Defaults live in a separate sibling module | Rejected. Adds module overhead for no real win; users still want zero-option `New` in core. |

### 3.5 Adapter dependency composition

When an adapter needs a service the engine already holds (e.g., an AuthZ adapter that wants to persist its policy in the same store the engine uses), the common case should be **zero-ceremony sharing**, with an explicit **split** when wanted. Options:

| Option | Description | Verdict |
|---|---|---|
| **A. Runtime service-locator on the concrete engine** | Adapter holds a reference to the concrete `*Thresher` and calls `engine.Repository()` at runtime. | Rejected. Couples the adapter to the concrete engine type; "where does this come from?" magic; hard to fake in isolation. |
| **B. Explicit user composition** | The user constructs the shared resource once and threads it into both the adapter and the engine. | Valid — kept as the full-isolation option. Most explicit; but it is ceremony for the common "just share the engine's default" case. |
| **C. Injected `EngineRuntime` interface** — chosen | The engine injects its resolved `EngineRuntime` (a core *interface* — §4.3) into adapters that opt in via the optional `RuntimeAware` hook (§8.3), at `thresher.New` assembly. An adapter pulls any dependency it wasn't explicitly given from the runtime (`rt.Repository()`); an explicit per-adapter option overrides it (the split). | Selected. Default = the adapter shares the engine's resources with no wiring; split = the adapter's own option. The handle is a core interface injected at assembly (not a concrete engine pulled at runtime), so the adapter stays unit-testable — fake the `EngineRuntime`. This keeps Option A's convenience while dissolving its coupling/testability objection. |

Example (Pattern C):

```go
// Default — AuthZ shares the engine's repository, zero ceremony.
// casbin.Authorizer implements RuntimeAware; the engine injects its
// EngineRuntime at New, and the adapter uses rt.Repository() for its storage.
authz, _ := casbin.NewAuthorizer()
engine, _ := thresher.New("my-engine",
    thresher.WithRepository(repo),                 // the app's default repo
    thresher.WithAuthorizationProvider(authz),     // engine injects EngineRuntime -> authz uses repo
)

// Split — give AuthZ its own store explicitly; the engine skips the injection.
authz, _ := casbin.NewAuthorizer(casbin.WithStorage(otherRepo))
engine, _ := thresher.New("my-engine",
    thresher.WithRepository(repo),
    thresher.WithAuthorizationProvider(authz),     // authz uses otherRepo, not repo
)
```

The adapter imports only core's `EngineRuntime` + `Repository` interfaces — never a concrete engine, never another adapter. The user keeps full control to split via the adapter's own option.

## 4. Decision Detail

### 4.1 Two-layer extension model

```
┌──────────────────────────────────────────────────────────────────┐
│                      Engine-level extensions                      │
│  (registered once at thresher.New via functional options;         │
│   scope = all instances of the engine; lifetime = engine lifetime)│
│                                                                   │
│  Repository, Logger, Tracer, MetricsRecorder, Clock,              │
│  MessageBroker, ExpressionEngine, AuthorizationProvider,          │
│  WorkerDispatcher, EventHub                                       │
└────────────────────────────┬─────────────────────────────────────┘
                             │ flows down into per-instance context
                             v
┌──────────────────────────────────────────────────────────────────┐
│              Instance-level context (RuntimeEnvironment)          │
│   (composed per Process Instance from engine-level extensions +   │
│    instance-scoped state; lifetime = instance lifetime)           │
│                                                                   │
│  Scope (instance-rooted)                                          │
│  InstanceID                                                       │
│  EventProducer (instance-scoped projection of EventHub)           │
│  RenderRegistrator (instance-scoped projection of TaskDistributor)│
│  (+ engine-level extensions accessible by reference)              │
└──────────────────────────────────────────────────────────────────┘
```

The instance-level `RuntimeEnvironment` is what nodes see during execution. Per [ADR-001 v.3](ADR-001-execution-model.md)'s two-layer model (Instance + track; token is a projection of a track's step), it's passed to tracks; tracks read it for scope lookups, event production, etc.

### 4.2 Engine-level extension catalogue

| Interface | Purpose | Default impl | Status vs current code |
|---|---|---|---|
| `Repository` | Persist Process Instance state, history, message inbox, wait subscriptions. The save/restore foundation per ADR-001 v.3 §4.7 (runtime invariants) + the Persistence & State ADR. | in-memory (non-durable) | NEW — does not exist |
| `Logger` (core interface; `*slog.Logger` satisfies it directly) | Structured logging | `slog.Default()` — visible by default per [project memory](../../) | NEW |
| `Tracer` (OTel-shaped, core-defined — **no OTel import**) | Distributed tracing spans | **no-op** (spans cost a per-event allocation, inert without a backend); opt in to an in-memory recent-spans ring or `adapters/otel/` | NEW |
| `MetricsRecorder` (OTel-shaped, core-defined — **no OTel import**) | Counter / histogram / gauge instruments | **in-memory queryable registry** — visible by default, cheap, series-capped, `Snapshot()` for tests/diagnostics; swap to no-op or `adapters/otel/` | NEW |
| `Clock` | Current time + sleep (testability for timers) | wall clock (`time.Now`) | NEW |
| `MessageBroker` | Incoming-Message inbox; correlation routing per [docs/bpmn-spec/semantics/correlation.md](../bpmn-spec/semantics/correlation.md) | in-memory inbox | NEW |
| `ExpressionEngine` | Evaluate `FormalExpression` (BPMN conditionExpression, gateway conditions, MI cardinality, etc.) | Go-native simple evaluator | EXTENDS — `data.FormalExpression` exists; engine wraps |
| `AuthorizationProvider` | Authorize sensitive ops (start Process, claim UserTask, cancel Instance, …) | "allow all" | NEW |
| `WorkerDispatcher` | Dispatch eligible Tasks (ServiceTask / GlobalTask) to remote workers per [SAD-001 §13.2](SAD-001-vision-and-architecture.md) | in-process local execution | NEW |
| `EventHub` | Central event distribution (existing rich interface) | in-memory hub (the current implementation, promoted to public default) | EXPOSE — currently `internal/eventproc.EventHub`; move interface to `pkg/`; implementation stays in `internal/` |
| `TaskDistributor` | UserTask routing to humans (composite of `Renderer` + `Registrator` concerns) | in-process registrator (the current `internal/interactor` impl) | EXPOSE — currently `internal/interactor`; promote interfaces |

**Interface-design principle.** Stick as tightly as possible to the established industry interface; only generalise further when there is no single obvious candidate.

- **`Logger`** — there is one obvious Go standard (`log/slog`). The core `Logger` is therefore a small leveled interface (`Debug`/`Info`/`Warn`/`Error(msg string, args ...any)`) that **`*slog.Logger` satisfies directly**, so the default is `slog.Default()` with no wrapper, while non-slog loggers can still be plugged.
- **`Tracer` / `MetricsRecorder`** — OpenTelemetry is the de-facto standard, so the core interfaces are **modeled on the OTel API shape** (span start/end/attributes/record-error; counter/histogram/gauge instruments). To preserve the stdlib-+-`uuid`-only core (SAD-001 G2), core does **not import** the OTel modules — it defines OTel-shaped interfaces, and the real OTel types live only in `adapters/otel/` as a thin pass-through.

**Default telemetry — chosen by signal cost (not a blanket no-op).** A blanket no-op leaves a zero-config engine blind (against the observability policy); a logging-backed default turns metrics into log text that must be parsed back out (garbage in the log stream). So defaults differ per signal:

- **Metrics → in-memory queryable registry, on by default.** Atomic counters + current-value gauges + fixed-bucket histograms, readable via `Snapshot()`. Increments are nanoseconds and the footprint is bounded — but it **caps total series** (counter-name × label-set), dropping-and-warning-once past the cap, so high-cardinality labels (`process_id`, `instance_id`) can't make it grow unbounded. Visible by default, structured (not log-scraped), and trivially assertable in tests. Swap to no-op to silence, or to `adapters/otel/` for production.
- **Tracing → no-op by default.** A span costs a per-event allocation and is inert without a consuming backend, so always-on tracing is exactly the "garbage configured by default" to avoid. A bounded in-memory **recent-spans ring** (last N, queryable) ships as a one-line opt-in for dev/debug, alongside the real `adapters/otel/`.
- **Persistent (DB) telemetry** is a production adapter (`adapters/sqlstore`), **never a core default** — it would add a driver dependency and grow unbounded; an embedder opts into that storage knowingly.

**In-memory defaults are bounded (cross-cutting principle).** The metrics series cap is one instance of a rule that governs *every* in-memory default: it bounds its own growth and **drops/evicts and warns once** past the cap, rather than growing without limit or failing silently. Visible-and-bounded beats both silent and unbounded; production swaps to a durable/external adapter that owns retention. Concretely:

- `Repository` (in-mem) — evicts terminal Instances and their history past a retention bound.
- `MessageBroker` (in-mem inbox) — bounds the inbox / expires uncorrelated messages (TTL).
- `TaskDistributor` (in-mem) — bounds pending (unclaimed) UserTasks.
- `EventHub` (in-mem) — bounded buffers with backpressure, not unbounded queues.
- `WorkerDispatcher` (in-process) — a bounded worker pool, not an unbounded goroutine-per-task.

The exact bound and eviction policy for each is settled at that extension's landing SRD (the skeleton SRD-004 ships the metrics series cap; the rest land with their execution wiring). **`AuthorizationProvider` is the exception** — its default question is not growth but *security posture*: the allow-all default delegates authorization to the host application, with deny-all the opt-in for a closed system. That is settled when authorization enforcement lands, not here.

### 4.3 RuntimeEnvironment interface — extended; Instance is the implementation

The existing `RuntimeEnvironment` in `internal/renv/renv.go` is already structured the right way: it's an interface, and `Instance` is the type that implements it. A track gets exactly one external reference at construction — its owning `*Instance` — and reaches everything it needs through that.

**This ADR just extends the existing RuntimeEnvironment interface with the new engine-level services.** No structural refactor of the Instance/track relationship; no second reference for the track; no forwarding accessor.

There are **two tiers**: the engine/server-level resolved configuration
(`EngineRuntime`) and the instance-level execution context
(`RuntimeEnvironment`), which embeds the former.

```go
// pkg/renv (moved from internal/renv, then extended)

// EngineRuntime — engine/server-level: the Thresher's RESOLVED extension set
// (the wired services). Thresher owns/implements it. Adapters receive it (§3.5);
// RuntimeEnvironment embeds it so tracks keep one uniform call style.
type EngineRuntime interface {
    Logger() Logger                              // core interface; *slog.Logger satisfies it
    Tracer() Tracer
    MetricsRecorder() MetricsRecorder
    Clock() Clock
    Repository() Repository
    ExpressionEngine() ExpressionEngine
    MessageBroker() MessageBroker
    AuthorizationProvider() AuthorizationProvider
    WorkerDispatcher() WorkerDispatcher
    EventHub() EventHub
    TaskDistributor() TaskDistributor            // was RenderRegistrator() in current code
}

// RuntimeEnvironment — instance-level execution context: engine services
// (embedded) + instance-local state. Instance implements it.
type RuntimeEnvironment interface {
    EngineRuntime                                // engine/server services (embedded)
    scope.Scope                                  // data scoping rooted at this instance
    InstanceID() string                          // instance identity
    EventProducer() EventProducer                // instance-scoped event production
}
```

Instance gets the engine-service methods **for free** by embedding the Thresher's
`EngineRuntime` value; it only adds its instance-local methods. No per-method
delegates.

```go
// internal/instance/instance.go (existing struct, extended)
type Instance struct {
    renv.EngineRuntime           // embedded: the Thresher's resolved EngineRuntime
    id          string
    scope       scope.Scope
    eventProd   EventProducer
    // ... per ADR-001 v.3
}

// Instance-local — direct (engine-level methods are promoted from the embedded
// EngineRuntime, so Logger()/Repository()/Clock()/... need no code here).
func (i *Instance) InstanceID() string           { return i.id }
func (i *Instance) Scope() scope.Scope           { return i.scope }
func (i *Instance) EventProducer() EventProducer { return i.eventProd }
```

Track call sites are uniform: one reference, one call style for everything.

```go
type track struct {
    inst *Instance                       // the ONLY external object track gets at construction
    // ... per ADR-001 v.3
}

// Uniform call style — track doesn't need to know which call is "instance" vs "engine":
t.inst.Scope().GetData(...)              // instance-local — Instance returns its own field
t.inst.ID()                              // instance-local
t.inst.Logger().Info(...)                // engine service — promoted from embedded EngineRuntime
t.inst.Clock().Now()                     // engine service — promoted from embedded EngineRuntime
t.inst.Repository()                      // engine service — promoted from embedded EngineRuntime
```

**Rationale for one-reference / Instance-as-RE model** (per user direction):

- The track already needs an Instance reference (for instance-scoped concerns like `Scope` and `ID`). Adding a SECOND reference for engine services duplicates plumbing for no gain.
- Instance is the natural composition point — it knows the instance AND holds a reference to the engine config.
- Track only ever has one external dependency: its owning Instance. Simpler for new contributors, simpler for testing, simpler in the goroutine plumbing per ADR-001 v.3.
- "Instance is for execution, not for holding runtime values" is satisfied by composition: Instance **embeds the Thresher's `EngineRuntime`** (the holder of the resolved values); the engine-level methods are promoted from it, not reimplemented. The runtime values are owned by the engine (`EngineRuntime`); Instance exposes them through embedding.

The existing pattern (Instance implements RuntimeEnvironment) is preserved; this ADR's contribution to it is just the extended interface (the additional engine-level method set).

### 4.4 Assembly pattern (functional options)

`thresher.New(id, ...Option)` is the only constructor. Zero options produces a working engine with all defaults; each option overrides one default.

```go
// Zero-config — defaults applied internally; works out of the box
engine, _ := thresher.New("my-engine-id")

// Custom configuration — options override individual defaults
engine, _ := thresher.New("my-engine-id",
    thresher.WithRepository(postgresRepo),
    thresher.WithLogger(slog.New(otelHandler)),
    thresher.WithTracer(otelTracer),
    thresher.WithMetricsRecorder(prometheusRecorder),
    thresher.WithClock(realClock),
    thresher.WithMessageBroker(redisBroker),
    thresher.WithAuthorizationProvider(authz),
    thresher.WithWorkerDispatcher(grpcDispatcher),
)
```

Each `WithXxx` is a `thresher.Option` returning a closure that mutates an internal config struct during `New()`. Options have NO ordering dependency unless explicitly documented; if `WithXxx` appears multiple times for the same extension, the last one wins (last-write semantics; standard functional-options convention).

Internally, `New` initializes the config with default values, applies each provided option in order, then logs the resolved configuration:

```go
func New(id string, opts ...Option) (*Thresher, error) {
    cfg := defaultConfig()           // ALL defaults wired here
    for _, opt := range opts {
        opt(&cfg)                    // override per option
    }
    t, err := assemble(id, cfg)
    if err != nil {
        return nil, err
    }
    t.logStartupConfig()             // INFO line — see §4.4.1
    return t, nil
}
```

This pattern means "default" is an internal implementation detail of `New`, not a user-facing alternative constructor. The public API is one function.

#### 4.4.1 Startup configuration logging

After `New` finishes wiring, Thresher emits a single INFO-level log line via the configured `Logger`, summarizing the resolved extension wiring. This gives ops a single-line answer to "what is this engine configured with?" at the moment of construction.

Format (illustrative — final structure pinned during implementation):

```
INFO thresher.starting
     id=my-engine
     repository=*memrepo.Repository
     logger=*slog.Logger(JSONHandler)
     tracer=noop.Tracer
     metricsRecorder=noop.MetricsRecorder
     clock=*systemclock.Clock
     messageBroker=*membroker.Broker
     expressionEngine=*goexpr.Engine
     authorizationProvider=*allowall.Provider
     workerDispatcher=*inproc.Dispatcher
     eventHub=*eventhub.Hub
     taskDistributor=*interactor.Distributor
```

Each value is the Go type of the wired implementation. The log line is structured (slog attributes), not free-form prose — downstream log processors can pivot on individual extension types.

Behavior:

- INFO level by default. The line is silent only if the user explicitly configures a Logger that discards INFO output. This aligns with the project's visible-by-default observability policy ([memory: observability policy](../../)).
- Emitted exactly once per `New` call, after options are applied but before the engine starts accepting work.
- Required, not optional. There is no `WithoutStartupConfigLog()` option — silencing it is the user's responsibility via their Logger configuration.

### 4.5 Default implementation policy

Every Engine-level interface has a default that:

- **Logger**: `slog.Default()` (visible by default per project policy — accidental silence is worse than accidental noise).
- **Tracer, MetricsRecorder, AuthorizationProvider**: no-op. Visible-by-default doesn't apply because Go stdlib has no sensible default for these; users opt in via adapters.
- **Repository, MessageBroker**: in-memory, non-durable. Suitable for tests / embedded short-lived use; production swaps via adapter.
- **Clock**: wall clock. Tests inject a fake clock for time-dependent BPMN behavior (Timer events).
- **WorkerDispatcher**: in-process local execution. The "distribution is opt-in" stance from SAD-001 §13.
- **ExpressionEngine**: minimal Go-native evaluator supporting simple expressions; users plug in JUEL / FEEL / etc. via adapter.
- **EventHub**: the current `internal/eventproc/eventhub` implementation, promoted as the default. The interface is public (`pkg/eventproc`); the implementation stays in `internal/`.
- **TaskDistributor**: the current `internal/interactor` implementation as default.

Defaults are bundled in core. Adapter modules (`adapters/*`) provide production implementations.

### 4.6 Adapter module conventions

Per SAD-001 §9.2 multi-module monorepo:

- Each adapter is its own Go module: `github.com/dr-dobermann/gobpm/adapters/<name>` with its own `go.mod`.
- An adapter MUST implement one or more public extension interfaces from core (`pkg/`).
- An adapter MUST NOT import any other adapter's package. This is the **no-cross-adapter-imports** rule.
- An adapter MAY default its dependencies from the injected `EngineRuntime` (§3.5 Pattern C / §8.3 `RuntimeAware`) and/or take explicit per-adapter options to override them (the split). Either way it depends only on core interfaces (`EngineRuntime`, `Repository`, …), never on another adapter or a concrete engine — the no-cross-imports rule holds.
- An adapter SHOULD declare its minimum compatible core version via `replace`-free pinning in its `go.mod`.
- An adapter's tests SHOULD verify against the contract published in this ADR (e.g., `Repository` impl must pass the same conformance test suite the in-memory default passes).
- Adapters MUST prefer **pure-Go embedded** implementations over service-dependent ones, to preserve the embeddable-library value proposition of core. Service-dependent adapters (gRPC sidecars, external HTTP services) are allowed but SHOULD be clearly labeled as such.

Initial adapter targets (illustrative; not authored in v.1 of this ADR):

- `adapters/postgres/` → `Repository` (pure-Go via `lib/pq` or `pgx`)
- `adapters/otel/` → `Tracer`, `MetricsRecorder` (pure-Go OpenTelemetry SDK)
- `adapters/oidc/` → identity claims (feeds into `AuthorizationProvider` context)
- `adapters/casbin/` → `AuthorizationProvider` (casbin is pure-Go in-process by default; service mode is opt-in and not the recommended path for embedded use)
- Simple-RBAC alternative → `AuthorizationProvider` (smaller embedded option for projects that don't need casbin's full policy language)
- `adapters/redis-broker/` → `MessageBroker` (service-dependent — would label as such)
- `adapters/nats-broker/` → `MessageBroker` (service-dependent)
- `adapters/feel/` → `ExpressionEngine` (FEEL evaluator, pure-Go)

The AuthZ adapter choice deserves a brief note: **casbin** is in fact pure-Go and runs in-process by default; the "casbin as service" mode is an opt-in deployment option, not the only path. So `adapters/casbin/` fits the "pure-Go embedded" preference. Smaller alternatives (`gorbac`, custom RBAC, embedded OPA) are equally valid choices and should be available — the AuthZ extension point is the interface, not any specific implementation.

### 4.7 Stability and versioning

Once this ADR is Accepted and the interfaces are published in `pkg/`:

- Each public extension interface is a **semver-stable contract**. The `gobpm` core module's major version expresses interface stability.
- **Backwards-compatible additions** (new methods on a new interface, new options) are MINOR version bumps.
- **Breaking changes** (changing an existing interface's method signatures, removing an extension) require a new ADR (or amended version of this one) AND a major version bump.
- Adapters declare their compatible core version range; major version mismatch is a compile-time failure.

Pre-1.0 (where we are): interface evolution is freer per Go's semver convention. The discipline above kicks in at v1.0.0.

## 5. Conception vs Current Code — Deliberate Departures

| Topic | Current code | This ADR | Required change |
|---|---|---|---|
| Engine constructor | `Thresher.New(id string)` — no options | `Thresher.New(id, opts ...Option)` — single constructor; zero options applies all defaults; each option overrides one default. No `NewDefault`. | Add `Option` type. Add functional-option implementations for each Engine-level extension. Refactor `New` to initialize defaults internally, then apply options. |
| RuntimeEnvironment interface scope | Current four methods: `scope.Scope` embed, `InstanceID()`, `EventProducer()`, `RenderRegistrator()` | **Extended** with engine-level service methods (`Logger`, `Tracer`, `Clock`, `Repository`, `ExpressionEngine`, `MessageBroker`, `AuthorizationProvider`, `WorkerDispatcher`, `EventHub`). `RenderRegistrator()` renamed to `TaskDistributor()`. | Add the engine-level methods to the RE interface. Move from `internal/renv` to `pkg/renv` (final path per ADR-003). |
| Instance-as-RE-implementation | Already done in current code | Preserved; track sees only `*Instance` and calls uniform method set | No relationship change — Instance continues to implement the (extended) RuntimeEnvironment interface. Add the new engine-level delegate methods to Instance, each forwarding to the engine config. |
| Thresher startup logging | No startup logging | Thresher emits one INFO-level structured log line summarizing the resolved extension wiring on every successful `New` call (§4.4.1) | Add `logStartupConfig` method to Thresher. Required behavior; cannot be opted out (user silences via their Logger config if desired). |
| Repository interface | Does not exist | Define `Repository` in `pkg/` with checkpoint / load / list-in-flight methods per ADR-001 v.3 §4.7 + the Persistence & State ADR | Implement in-memory default. Add to `Thresher` config. |
| Logger / Tracer / MetricsRecorder | Do not exist | `Logger` = slog-satisfiable core interface; `Tracer`/`MetricsRecorder` = OTel-shaped core interfaces (no OTel import — SAD-001 G2) | Defaults: `slog.Default()` Logger; **in-memory queryable registry** Metrics (series-capped, `Snapshot()`); **no-op** Tracer (in-mem span-ring + OTel are opt-in). Real OTel in `adapters/otel/`. |
| Clock | Does not exist | Define `Clock` interface in `pkg/` | Implement wall-clock default. Inject into Timer event handling. |
| MessageBroker | Does not exist | Define in `pkg/` per [bpmn-spec/semantics/correlation.md](../bpmn-spec/semantics/correlation.md) | Implement in-memory inbox default. |
| AuthorizationProvider | Does not exist | Define in `pkg/`; hook points at sensitive ops | Implement allow-all default. Identify hook-point call sites. |
| WorkerDispatcher | Does not exist | Define in `pkg/` per [SAD-001 §13.2](SAD-001-vision-and-architecture.md) | Implement in-process dispatch default. |
| ExpressionEngine | Partial: `data.FormalExpression` in `pkg/model/data/` | Wrap `FormalExpression` evaluation in `ExpressionEngine` interface at `pkg/` level | Promote / add ExpressionEngine. Default uses existing Go evaluator. |
| EventHub interface location | `internal/eventproc.EventHub` (not externally implementable) | Move interface to `pkg/` (`pkg/eventproc` per ADR-003); keep implementation in `internal/` | Split interface from implementation; redirect imports. |
| TaskDistributor / RenderRegistrator | `internal/interactor.Registrator` + `Renderer` ecosystem | Promote to `pkg/`; rename `Registrator` → `TaskDistributor` for clarity; preserve `Renderer` abstraction | Move + rename. Update `RuntimeEnvironment.RenderRegistrator()` → `TaskDistributor()`. |
| RuntimeEnvironment location | `internal/renv` | Move to `pkg/` (likely `pkg/renv`) | Move. Update Instance + track to import from new location. |
| Extension docs | Scattered through code comments | Single canonical extension catalogue in this ADR | Maintain this ADR as the catalogue source of truth. |

**How these land (resolves the ordering with §7).** The departures are implemented together — at **minimal / default behavior only** — in a **single foundational SRD** (the "extension skeleton"): every interface defined in `pkg/`, each with its bundled default impl, the functional-options assembly, the extended `RuntimeEnvironment`, and the startup log line, wired so the engine runs end-to-end on today's BPMN support. **This ADR flips Draft → Accepted when that one SRD lands and its §7 tests pass** (not before — see §7). Production adapters and per-interface depth (durable Repository, real MessageBroker, FEEL ExpressionEngine, remote WorkerDispatcher, …) follow as later, separately-gated SRDs. The exact package paths (where in `pkg/` each interface lives) are deferred to ADR-003 Module Layout.

## 6. Consequences

### 6.1 Pros

- **Out-of-the-box usability preserved.** Zero-option `thresher.New(id)` gives a working engine in one call (defaults are the default; no separate `NewDefault`).
- **Extension matrix is documented.** Future contributors and adapter authors have a single source of truth for what's pluggable.
- **Go-idiomatic.** No frameworks, no DI containers, no plugin loaders — just interfaces + functional options.
- **Stable public surface.** Public interfaces in `pkg/` carry semver contract; internal implementation can evolve freely.
- **Adapter ecosystem enabled.** Clear contract for what adapter modules deliver and how they declare compatibility.
- **Observability visible-by-default.** Aligns with project policy; accidental silence avoided.

### 6.2 Cons

- **More public surface area to maintain.** 10+ extension interfaces become stability contracts at v1.0. Each interface change requires care.
- **Default implementations bundled in core inflate the core module's surface.** Mitigated by keeping defaults small and well-isolated (one file per default).
- **Interface naming will track industry conventions.** Some names carry historical baggage (`Registrator`, and the SAD's `WorkerDispatcher` / `TaskDistributor` vs alternatives like `ServiceTaskExecutor`). Renaming the extension interfaces toward established industry vocabulary during the implementation phase is **accepted and expected** — they are not contract-frozen until this ADR is Accepted. **Exception — the concrete engine type stays `Thresher`:** it is deliberately distinctive; `Server` and `Engine` are too generic to name the type and are reserved as *role* words (e.g., the engine-level `EngineRuntime` interface — "the runtime contract of the engine"), never the type's own name.
- **Some current internal helpers move to `pkg/`** — once public, they can't be refactored without ADR amendment.

### 6.3 Implications for adjacent decisions

- **ADR-003 Module Layout**: defines where exactly each interface lives in the `pkg/` subdirectory tree. Likely candidates: `pkg/extension/` (single subpackage), or one subpackage per concern (`pkg/persistence`, `pkg/observability`, `pkg/auth`, `pkg/expression`, etc.).
- **ADR-004 Runtime Environment Contract**: runtime layer wires production-grade adapters (postgres + otel + oidc + casbin + …) into the Engine via these extension options.
- **ADR-001 Execution Model (v.3)**: `Repository` is the persistence interface the runtime invariants in ADR-001 v.3 §4.7 (and the Persistence & State ADR) target. `Logger` / `Tracer` / `MetricsRecorder` consume the instance's runtime event stream — the single `trackEvent` stream and the token-worded view derived from it (ADR-001 v.3 §4.3).

## 7. Verification

How we'll know the extension architecture works:

| What | How |
|---|---|
| **Zero-option New works end-to-end** | Integration test: `thresher.New("test").Run(ctx)`; register a process; start an instance; verify it completes. Defaults are wired internally; zero user-side configuration. |
| **Functional options compose without ordering issues** | Test: construct an Engine with all 10 `WithXxx` options in random orders; assert resulting Engine state is identical. |
| **Each option overrides the default** | Per-option test: construct with `WithLogger(custom)`; assert engine's Logger is `custom`, not `slog.Default()`. Repeat for each interface. |
| **Last-write semantics** | Test: pass `WithLogger(A), WithLogger(B)`; assert engine uses B. |
| **Cross-adapter composition** | Test (with a real/fake `RuntimeAware` adapter): given no storage option, the AuthZ adapter uses the engine's `Repository` via the injected `EngineRuntime` (default share); given `WithStorage(otherRepo)` it uses that instead (split). Verifies §3.5 Pattern C / §8.3. |
| **Startup config log line** | Test: construct engine with a Logger that captures records. Assert: exactly one INFO-level record with key `thresher.starting` is emitted, containing attributes for every Engine-level extension (`repository`, `logger`, `tracer`, `metricsRecorder`, `clock`, `messageBroker`, `expressionEngine`, `authorizationProvider`, `workerDispatcher`, `eventHub`, `taskDistributor`) with values matching the wired implementation type names. Verifies §4.4.1. |
| **Instance implements RuntimeEnvironment** | Type assertion test: `var _ RuntimeEnvironment = (*Instance)(nil)`. Compile-time check that Instance satisfies the extended interface. |
| **Instance engine-service delegates** | Per-method test: construct engine with custom Logger (or Clock, or Repository, …); spawn an Instance; assert `instance.Logger()` (etc.) returns the same value as the engine config holds. Verifies one-line delegate correctness. |
| **Default impls match the public interface contract** | Conformance test: in-memory Repository default passes the same conformance suite that a hypothetical postgres Repository would. Same for MessageBroker, etc. |
| **Engine without optional extension still works** | Smoke test: omit every optional `WithXxx`; verify the zero-option `New(id)` engine runs. |
| **Adapter module isolation** | When adapter modules exist: importing only `core` does NOT transitively pull `adapters/*` deps. Verified via `go mod graph`. |
| **RuntimeEnvironment composition correct** | Test: spawn an Instance; assert its `RuntimeEnvironment.Logger()` is the engine's Logger; assert `Scope` is instance-rooted; assert `EventProducer` is instance-scoped. |

**Acceptance gate** (Draft → Accepted): these tests MUST exist and pass against the **foundational extension-skeleton SRD** (§5) — the defaults-only implementation. The two **adapter-dependent** rows (*cross-adapter composition*, *adapter module isolation*) can only run once a real adapter module exists; they are deferred to the first adapter's SRD and are **not** required for this ADR's acceptance. Until the skeleton SRD lands and its applicable tests pass, the ADR remains Draft.

## 8. Enterprise-Readiness Recommendations

This section captures cross-cutting best practices for adapter authors and production deployments. These are advisory — not normative — but each one closes a class of operational failure observable in real BPM-engine deployments. The recommendations are written assuming the project is moving from research-phase to production-phase use.

### 8.1 Observability conventions

A consistent observability vocabulary across Logger, Tracer, and MetricsRecorder is the difference between "diagnose in minutes" and "diagnose in days." Standardize three things:

**Logger attribute keys** (slog conventions; stable, documented names):

| Attribute | Always present in | Example value |
|---|---|---|
| `gobpm.engine_id` | All engine-emitted records | `"my-engine"` |
| `gobpm.instance_id` | Instance-scoped records | UUID |
| `gobpm.process_id` | Instance-scoped records | `"order-fulfillment"` |
| `gobpm.track_id` | Track-scoped records | UUID |
| `gobpm.token_id` | Token-related records | UUID |
| `gobpm.node_id` | Node execution records | `"ServiceTask_ChargeCard"` |
| `gobpm.element_type` | Node execution records | `"ServiceTask"` |
| `trace_id`, `span_id` | When Tracer is wired (OTel-compatible) | hex strings |
| `tenant_id` | When tenancy wired (per ADR-004) | tenant identifier |

These keys appear in production logs from day one. Skipping them during research-phase development creates blind spots that hit hard during the first real production incident.

**On the choice of tracing standard.** OpenTelemetry is the only viable open standard for distributed tracing at this point. The predecessors (OpenTracing, OpenCensus) merged into OTel; vendor-specific SDKs (Datadog APM, New Relic Go agent) carry lock-in. We define our own `Tracer` interface in `pkg/` (per §4.2) rather than re-exporting OTel types directly — this preserves freedom to swap the tracing backend if the landscape changes, and keeps `core` dependency-free per SAD-001 G2. The default `Tracer` adapter wraps OTel; users who need a different backend write their own adapter. Method signatures of the `Tracer` interface SHOULD mirror OTel's span vocabulary (start span, set attributes, record error, end) to minimize impedance mismatch.

**Tracer span hierarchy** (maps the BPMN execution tree to OTel-style spans):

```
thresher.engine.run
  └─ thresher.instance.run        (per Process Instance)
       └─ thresher.track.execute  (per track per ADR-001 v.3)
            └─ thresher.step      (per node visited)
                 └─ child spans   (HTTP / DB / etc. — user code)
```

Each span SHOULD carry the same attribute keys as §8.1 logger attributes. Standard OTel `process.*`, `db.*`, `http.*` semantic conventions apply for external calls within a node. Span status: ERROR on track failure; recovery via interrupting boundary event resets to OK with audit-trail noting the original error.

**Metric naming** (Prometheus / OTel-aligned, `gobpm_*` prefix):

| Metric | Type | Labels |
|---|---|---|
| `gobpm_instances_active` | Gauge | `engine_id`, `process_id` |
| `gobpm_instances_completed_total` | Counter | `engine_id`, `process_id`, `outcome` (`normal` / `terminated` / `failed`) |
| `gobpm_tokens_active` | Gauge | `engine_id`, `process_id` |
| `gobpm_track_duration_seconds` | Histogram | `engine_id`, `process_id`, `element_type` |
| `gobpm_repository_op_duration_seconds` | Histogram | `op` (`checkpoint` / `load` / `list_inflight`) |
| `gobpm_message_correlation_attempts_total` | Counter | `outcome` (`matched` / `no_match` / `ambiguous`) |
| `gobpm_authz_decisions_total` | Counter | `outcome` (`allow` / `deny` / `error`) |

Adapters MAY register their own metrics under their adapter's sub-namespace (e.g., `gobpm_postgres_connection_pool_busy`) via the same `MetricsRecorder`.

### 8.2 Adapter operational expectations

Production-grade adapters carry operational burden the in-memory defaults don't. Documenting these expectations up front prevents the "the integration tests pass but it falls over in prod" trap.

**Repository:**
- Connection pooling. Per-call connect-disconnect dies under load.
- Per-op timeouts. A wedged DB MUST NOT block the engine indefinitely. Time out and surface the error to the engine's error path.
- Idempotent checkpoint operations. Re-running a checkpoint for the same `(instance_id, state_version)` MUST produce the same persisted state.
- Schema migration tooling. Adapter SHOULD ship explicit `Migrate(ctx)` rather than auto-apply on startup — production deployments often want manual control.
- Pool-health metric exposure. Operators need to see pool exhaustion before it becomes an outage.

**MessageBroker:**
- Delivery semantics MUST be documented. "At-least-once" is the floor; "exactly-once" requires documented dedup mechanism.
- Correlation matching is the engine's job; adapter MUST NOT do correlation-level filtering.
- Out-of-order arrivals MUST be tolerated by the engine. Adapters MAY enforce ordering for specific patterns but MUST NOT assume the engine relies on it.
- Dead-letter routing for uncorrelatable messages — production adapters SHOULD provide; default in-memory MAY omit.

**AuthorizationProvider:**
- Decision caching with short TTL (~60s typical). Policies don't change every request.
- Cache-bust API for policy-change scenarios.
- Fail-closed on adapter error (deny, not allow). Document explicitly.
- Decision metric (`gobpm_authz_decisions_total{outcome}`) for ops visibility.

**WorkerDispatcher:**
- Worker heartbeat / liveness tracking. Failed worker → re-dispatch to a healthy one.
- Capability-based routing — worker registers what it can execute; dispatcher matches.
- Per-task timeout enforced by dispatcher, not by engine — engine doesn't know remote-worker SLAs.

### 8.3 Optional side-capability interfaces

Some adapters need lifecycle integration with the engine. Rather than overloading the core extension interfaces, define optional interfaces that the engine detects via type assertion:

```go
// pkg/extension (or wherever the contracts live)

// Optional — adapters that need explicit startup
type Starter interface {
    Start(ctx context.Context) error
}

// Optional — adapters that need explicit shutdown
type Stopper interface {
    Stop(ctx context.Context) error
}

// Optional — adapters that expose health
type HealthChecker interface {
    HealthCheck(ctx context.Context) error
}

// Optional — adapters that want the engine's resolved services. The engine
// injects its EngineRuntime (§4.3) at New; the adapter uses it to default any
// dependency it wasn't explicitly configured with (e.g. rt.Repository()).
// See §3.5 Pattern C.
type RuntimeAware interface {
    UseRuntime(rt EngineRuntime)
}

// Optional — adapters that declare their cluster-mode compatibility
type ClusterAware interface {
    // ClusterCompatibility returns whether this adapter is safe to use when
    // the runtime is configured in cluster mode. On false, reason explains
    // why (e.g., "in-memory; state not shared across nodes"). The runtime
    // refuses to start in cluster mode if any wired adapter declares (false, _).
    // Adapters that don't implement this interface get a startup warning in
    // cluster mode (compatibility undeclared); they're not blocked.
    ClusterCompatibility() (compatible bool, reason string)
}
```

When Thresher constructs and runs, it detects whether each registered extension implements one of these and integrates accordingly:
- `UseRuntime` is called during `New`, after the engine resolves its config, on each wired adapter that implements it — passing the engine's `EngineRuntime` so the adapter can default its dependencies from the engine (§3.5 Pattern C).
- `Start` is called during `Run` setup before instances are accepted.
- `Stop` is called during engine shutdown after all instances are drained or terminated.
- `HealthCheck` is exposed by the runtime layer (per ADR-004) for liveness/readiness endpoints.
- `ClusterCompatibility` is queried by the runtime layer at startup when cluster mode is active; any `(false, reason)` return is a hard startup failure. (Substantive cluster design lives in future ADR-008; per [SAD-001 §13.5](SAD-001-vision-and-architecture.md).)

Adapters that don't implement them just work. This is progressive enhancement — small adapters stay simple; large adapters get lifecycle hooks when they need them.

### 8.4 Adapter contract testing

The single most effective tool for keeping adapter implementations honest: **every adapter passes the same conformance test suite as the in-memory default.**

This is a standard Go testing pattern — no new framework, no special infrastructure. Each public extension interface ships alongside a published conformance helper: an ordinary exported function that takes `*testing.T` and a factory function for the implementation under test, then runs through the contract via subtests. Adapter tests are one-liners calling the helper.

```go
// Core publishes a conformance helper alongside each extension interface.
// (Exact package location per ADR-003 Module Layout — uses standard Go
// testing.T + table-driven subtests; no special framework.)
func RepositoryConformance(t *testing.T, factory func() Repository) {
    // covers the full Repository contract:
    // checkpoint, load, list-in-flight, idempotency, concurrent access,
    // error paths, large-payload handling, …
}

// Adapter test code is trivial:
func TestPostgresRepository(t *testing.T) {
    RepositoryConformance(t, func() Repository {
        return postgres.NewRepository(testConnString)
    })
}
```

Established Go projects use exactly this pattern (`database/sql/driver/driverstest`, `golang.org/x/oauth2/internal/tokenstest`, etc.). Standard idiom, no reinvention. The conformance helper is just exported test code.

Each major extension type SHOULD have a published conformance helper: `RepositoryConformance`, `MessageBrokerConformance`, `LoggerConformance`, `ClockConformance`, etc. The exact package location is deferred to ADR-003 Module Layout — but it lives in a test-importable location (likely a `*_test_helpers` subpackage or similar Go-idiomatic split).

### 8.5 Audit vs ops event separation

Two distinct concerns are derived from the **single** in-memory runtime event stream (ADR-001 v.3 §4.3 — `trackEvent`, track → loop; there is no second live channel). They are two *views* of that stream, not two channels:

| Concern | View / source | Durability | Examples |
|---|---|---|---|
| **Audit events** — compliance, must-not-lose | the **BPMN-observable, token-worded view** derived from the stream (split / merged / waiting / consumed / withdrawn — ADR-001 v.3 §4.3) | Durable; persistent subscriber required | "User X claimed UserTask Y", "Process started by Z", "Authorization denied: user=A action=cancel resource=instance/123" |
| **Ops events** — diagnostics, may-lose-acceptable | the **raw track/step transitions** (`trackEvent` + track state machine — ADR-001 v.3 §4.2/§4.3) | Best-effort; Logger/Tracer/MetricsRecorder subscribers | "track entered TrackExecutingStep", "StepPrologued completed", "fork spawned a new track" |

Audit subscribers SHOULD use durable transport (DB write per event, Kafka with ack, etc.). Ops subscribers MAY use best-effort transport (in-memory channels, UDP, fire-and-forget).

Mixing the two (audit fields included in ops logs; ops noise included in audit trail) creates compliance friction (auditors don't want ops noise) and ops friction (audit channel becomes too quiet to diagnose with).

**Note (re-grounded on the two-layer model).** ADR-002's earlier draft mapped "TokenEvent = audit / TrackEvent = ops" onto ADR-001's then-tentative three-layer model; ADR-001 v.3 collapsed to two layers (token is a projection, not a stored object / separate event channel). So the split is re-stated above as **two derived views of the one `trackEvent` stream** — audit = the token-worded projection, ops = the raw track/step transitions. The withdrawn/merged distinctions and any future compliance-relevant token states (e.g. "claimed", "delegated", "escalated") are produced by the gateway/events ADRs ([ADR-005](ADR-005-gateways-and-joins.md)/[ADR-006](ADR-006-events-and-subscriptions.md)); the audit view extends as those land. The boundary is provisional, not locked.

### 8.6 Backwards compatibility, deprecation, and sensitive data

**Backwards compatibility discipline** (post-1.0; relaxed pre-1.0):

- **Add-only changes** to public interfaces — new methods on new interfaces, new options. Minor version bump.
- **Behavior stability** — changing what an existing method returns under existing inputs is a breaking change, even if the signature is unchanged.
- **Deprecation path** — rather than removing a method, mark `// Deprecated:` with a removal version. Keep the deprecated method for at least one minor version after introduction of the replacement.
- **Adapter version negotiation** — adapters declare min-compatible-core via build tags or `go.mod` constraints; runtime detects mismatch and refuses to start.

Bake the discipline in early — retrofitting it after a real user complains is harder than starting with it.

**Sensitive data handling**:

BPMN Process variables can carry PII / regulated data. The engine itself doesn't classify; classification, redaction, encryption are adapter responsibilities.

- `Logger` adapters SHOULD support field-level redaction policy (e.g., `gobpm.process_variable.customer_email` redacted at INFO; full at DEBUG with caller-required permission).
- `Repository` adapters SHOULD support encryption-at-rest for the variables column / equivalent storage.
- Audit subscribers SHOULD support immutable append-only mode for compliance contexts (SOC2, GDPR, HIPAA).
- The token-worded audit view (derived from the `trackEvent` stream — §8.5) is the natural audit feed; the engine does not need to know which fields are sensitive — the audit adapter applies its own classification per organizational policy.

This separation lets one runtime serve both "no classification needed" (internal automation) and "strict classification required" (regulated customer-facing apps) deployments without engine-level changes.

## 9. References

- [SAD-001 Vision & Architecture](SAD-001-vision-and-architecture.md) — §11 Extension Model (this ADR refines); §6 Quality Attributes; §13 Distribution & Scale (preliminary)
- [ADR-001 v.3 Execution Model](ADR-001-execution-model.md) — the runtime this extends: §4.7 runtime invariants the Repository persists (+ the Persistence & State ADR for durable checkpoint/rehydrate); §4.3 the single `trackEvent` stream + derived token-worded view that Logger / Tracer / MetricsRecorder / audit subscribers consume
- [docs/bpmn-spec/semantics/correlation.md](../bpmn-spec/semantics/correlation.md) — MessageBroker contract for Message correlation
- [docs/bpmn-spec/semantics/data.md](../bpmn-spec/semantics/data.md) — ExpressionEngine integration (FormalExpression evaluation)
- Existing code:
  - `pkg/thresher/thresher.go` — current Engine façade
  - `internal/eventproc/eventproc.go` — existing event distribution interfaces (EventHub etc.)
  - `internal/scope/scope.go` — existing Scope interface
  - `internal/renv/renv.go` — existing RuntimeEnvironment composite
  - `internal/interactor/interactor.go` — existing human-interaction abstraction
  - `pkg/model/data/` — FormalExpression and related model types

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-05-30 | Ruslan Gabitov | Initial Draft. Pre-acceptance iteration ongoing; pre-version amendments are folded into this Draft without per-round history rows (per project discipline: history captures version snapshots, not brainstorming). When v.1 flips to Accepted, this row records the Accepted state. |
