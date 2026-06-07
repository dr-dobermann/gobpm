# ADR-004 — Runtime Environment Contract

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-05-31 |
| Owner | Ruslan Gabitov |
| Supersedes | — |
| Refines | [SAD-001 v.1 §12 Runtime Environment](SAD-001-vision-and-architecture.md) |

## 1. Context

`gobpm` core is an embeddable Go library. Some users will embed it directly in their applications and never need a "server" wrapper. Other users need a standalone process they can deploy, point clients at over HTTP/gRPC, integrate with corporate identity providers, surface observability through their existing OTel stack, and operate via diagnostic endpoints.

The `runtime/` submodule (scaffolded per [SAD-001 §9.2](SAD-001-vision-and-architecture.md) and [ADR-003 §4.6](ADR-003-module-layout.md)) is where that "standalone server" wrapper lives. SAD-001 §12 listed the runtime's responsibilities at a conceptual level; this ADR locks the concrete contract:

- What the runtime layer is responsible for, and what it explicitly is NOT responsible for (those concerns stay in core).
- The lifecycle: startup wiring, shutdown ordering, graceful drain.
- The API surface: HTTP REST + gRPC, what each is used for.
- Tenancy propagation: how multi-tenant context flows from request through engine.
- AuthN and AuthZ integration patterns.
- Observability stack wiring: OTel adapters configured by runtime, consumed by core via ADR-002 interfaces.
- Diagnostic and operational endpoints.
- Health-check endpoints and the [ADR-002 §8.3](ADR-002-extension-architecture.md) `Starter` / `Stopper` / `HealthChecker` integration.
- Configuration model.

Out of scope for v.1 of this ADR: multi-node distribution (deferred to a future ADR-008, per [SAD-001 §13](SAD-001-vision-and-architecture.md) preliminary section).

Current code state: `runtime/` does not exist yet. Per ADR-003, it is scaffolded as the first migration step (empty `go.mod` + `doc.go` + stub `cmd/gobpm-server/main.go`). This ADR defines what fills the scaffold.

## 2. Decision

**The `runtime/` submodule provides a thin standalone-server wrapper around `gobpm` core. It owns multi-tenant context extraction, AuthN integration, observability adapter wiring, REST + gRPC API surfaces, diagnostic endpoints, health checks, and the engine lifecycle (startup ordering, graceful drain on shutdown). It does NOT reimplement engine semantics, does NOT define new BPMN behavior, and does NOT carry business-logic that should live in core.**

Summary table:

| Concern                           | Owned by runtime?  | Mechanism                                                                                                            |
| --------------------------------- | ------------------ | -------------------------------------------------------------------------------------------------------------------- |
| HTTP REST API                     | YES                | Go HTTP router + middleware chain (router library choice deferred to SRD; current preference: echo)                  |
| gRPC API                          | YES                | `google.golang.org/grpc` with interceptor chain                                                                       |
| Multi-tenant context extraction   | YES                | HTTP middleware / gRPC interceptor extracts tenant ID from request → `context.Context` → engine receives ctx         |
| AuthN                             | YES                | Multiple AuthN providers active simultaneously with per-service-group selection (user-facing JWT, service-to-service mTLS, diagnostic tokens, …); adapter modules MAY register their own providers — §4.7 |
| AuthZ                             | Delegated to core  | Runtime supplies actor + action + resource; core's `pkg/auth.AuthorizationProvider` makes the decision (per ADR-002) |
| Observability adapter wiring      | YES                | Runtime constructs Tracer / MetricsRecorder via adapter modules; passes to core via `thresher.WithXxx(...)` options  |
| Diagnostic API                    | YES                | Diagnostic service group: instance state inspection, token positions, history queries, manual intervention — §4.10  |
| Health checks                     | YES                | Liveness + readiness; readiness consults adapter `HealthChecker`s per ADR-002 §8.3                                   |
| Engine lifecycle                  | YES                | `Starter` adapters run on startup; `Stopper` adapters run on shutdown; graceful drain on SIGTERM                     |
| Engine semantics (BPMN execution) | NO — stays in core | Runtime hosts but does not replicate                                                                                 |
| Persistence                       | NO — adapter       | `pkg/repository.Repository` impl wired by user / runtime config                                                      |
| Tenancy enforcement policy        | Hybrid             | Runtime extracts and propagates tenancy; core's `Repository` adapter enforces scoping in queries                     |

## 3. Alternatives Considered

### 3.0 Note on scope of this ADR

This is a conceptual ADR — service groups, contracts between layers, lifecycle ordering, security model. **Concrete endpoint shapes (paths, methods, request/response schemas, protobuf service definitions), specific library choices (HTTP router, config loader, etc.), and the exact REST/gRPC split per service group are SRD-level concerns** — each service group has its own implementation SRD when it's scheduled. Examples in this ADR are illustrative, not prescriptive.

### 3.1 API surface — HTTP only vs gRPC only vs both

| Option                                             | Description                                                                                                                                                                                                                                   | Verdict                                                                                                                                          |
| -------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| **HTTP REST only**                                 | One protocol, simpler ops                                                                                                                                                                                                                     | Rejected. Streaming endpoints (event tails, history feeds, audit streams) work poorly over HTTP without server-sent-events or WebSocket bolt-on. |
| **gRPC only**                                      | Modern, streaming-native                                                                                                                                                                                                                      | Rejected. Higher friction for casual clients (curl, browser tools); operators expect REST for one-off probes.                                    |
| **Both REST and gRPC, per-service-group choice (TBD per SRD)** — chosen | REST and gRPC are both first-class transports. Each service group (§4.5) MAY be delivered over REST, over gRPC, or both at parity. Initial intent: REST is the more common transport for human-facing and ops calls; gRPC excels for streaming and high-throughput service-to-service. **Exact transport-per-group is deferred to each group's implementation SRD.** Worker dispatch over REST and execution/diagnostic over gRPC are both possible and may land later. | Selected. Standard pattern in production engines (Camunda, Temporal); both protocols where each fits best; final split decided per group at SRD time. |

### 3.2 Multi-tenancy model

| Option                                                      | Description                                                                                                                                                  | Verdict                                                                                                                                                                                                 |
| ----------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **One Engine instance per tenant**                          | Separate `Thresher` for each tenant, full isolation at engine level                                                                                          | Rejected. Operational overhead scales with tenant count; cross-tenant background tasks (timers across tenants, signal broadcast across same-tenant subset) become awkward. Memory footprint multiplied. |
| **Single Engine, tenant-aware Repository + AuthZ** — chosen | One `Thresher` for everything; tenant ID flows via `context.Context`; Repository filters by tenant on every query; AuthZ enforces cross-tenant access denial | Selected. Single-engine cost; tenant isolation enforced at the data-access boundary; standard Go idiom (ctx propagation).                                                                               |
| **Tenant-namespaced Process IDs**                           | Tenant ID embedded in Process ID strings                                                                                                                     | Rejected. Workaround pattern; doesn't enforce isolation at the engine layer; allows accidental cross-tenant reads if a code path forgets to namespace.                                                  |

### 3.3 AuthN provider integration

| Option                                             | Description                                                                                                                                                         | Verdict                                                                                                                                          |
| -------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Built-in user database** | Runtime has its own users + passwords | Rejected. Reproduces enterprise IdP that already exists; carries password storage risk; not what corporate ops wants. |
| **Single global AuthN provider** | One provider enforced for all endpoints | Rejected. Different APIs need different auth: human-facing endpoints want JWT/OIDC, service-to-service workers want mTLS, diagnostic operations want elevated tokens. Forcing one provider for all defeats the use cases. |
| **Strict bearer-token only** | All requests carry JWT; runtime validates | Rejected as ONLY option. JWT is one provider category; the architecture needs more. |
| **Multiple simultaneous AuthN providers with per-service-group selection** — chosen | Runtime defines `AuthN` interface. Multiple providers may be active at once (OIDC + mTLS + service tokens + …). Each service group (§4.5) declares which provider categories it accepts; runtime's middleware chain consults the eligible providers per request; first non-nil Identity wins. Service-to-service AuthN is first-class. | Selected. Same Ports-and-Adapters pattern as core extensions; covers human-facing + service-to-service + elevated-diagnostic separately; aligns with existing IAM ADR's "Identity Service" abstraction. |
| **Module-registered AuthN providers** (additive to chosen) | Adapter modules can register their own AuthN providers via a runtime registration interface during startup. Deployments configure which registered providers apply to which service group. | Selected as additive — runtime exposes a provider registry; modules register before listeners open (during startup Phase 2). |

### 3.4 Startup / shutdown ordering

| Option | Description | Verdict |
|---|---|---|
| **Implicit ordering** | Constructor runs everything; deinit reverses | Rejected. Adapter dependencies (e.g., Logger needed before Repository emits any error logs) aren't honored. |
| **Explicit phased startup** — chosen | Phase 1: Logger; Phase 2: Tracer / MetricsRecorder; Phase 3: Repository + MessageBroker + Clock + AuthZ + ExpressionEngine; Phase 4: WorkerDispatcher + TaskDistributor; Phase 5: HTTP / gRPC listeners; Phase 6: signal handler for graceful shutdown | Selected. Observability up first so subsequent startup failures are diagnosable. Listeners last so the engine is fully ready before accepting work. |

### 3.5 Configuration model

| Option | Description | Verdict |
|---|---|---|
| **Structured config file (e.g., YAML or TOML) with env-var expansion** — chosen | `gobpm-server --config=<path>` reads a hierarchical config (one section per concern: engine, observability, repository, authn, …). Env-var expansion (`${VAR}`) for secrets. Specific format and loader library deferred to implementation SRD. | Selected as primary path. Standard ops pattern; library choice (koanf, viper, alternatives) is SRD-level. |
| **All-via-env-vars** | 12-factor style | Supported but not primary. Too many config options for clean env-var representation. Env vars complement the config file for secrets and per-deployment overrides. |
| **Programmatic only** | User writes Go code that constructs the runtime | Rejected as primary. The runtime IS the "I don't want to write Go to start a server" entry point. Programmatic construction (via the runtime's library API) remains available for advanced users. |

## 4. Decision Detail

### 4.1 Runtime layer responsibilities

```
┌──────────────────────────────────────────────────────────────────┐
│  Runtime (runtime/ submodule)                                     │
│                                                                   │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │  API surface                                              │    │
│  │    HTTP REST  /api/v1/...                                 │    │
│  │    gRPC       streaming + high-throughput                 │    │
│  │    Health     /healthz, /readyz                           │    │
│  │    Profiling  /debug/pprof/*                              │    │
│  └────────────────┬────────────────────────────────────────┘    │
│                   │                                                │
│  ┌────────────────┴────────────────────────────────────────┐    │
│  │  Middleware / interceptor chain                           │    │
│  │    Request logging                                         │    │
│  │    Trace context extraction (OTel)                         │    │
│  │    Tenant ID extraction → context.Context                  │    │
│  │    AuthN: identity extraction (OIDC, JWT, mTLS, …)         │    │
│  │    Rate limiting / throttling                              │    │
│  └────────────────┬────────────────────────────────────────┘    │
│                   │                                                │
│  ┌────────────────┴────────────────────────────────────────┐    │
│  │  Engine adapter (thin wrapper)                            │    │
│  │    Construct thresher.Thresher with wired adapters        │    │
│  │    Manage lifecycle (Starter / Stopper sequencing)        │    │
│  │    Provide engine handle to API handlers                  │    │
│  └────────────────┬────────────────────────────────────────┘    │
│                   │                                                │
└───────────────────┼────────────────────────────────────────────────┘
                    │
                    v
        ┌─────────────────────────┐
        │   gobpm core (pkg/)     │
        │   - thresher.Thresher   │
        │   - Engine semantics    │
        └─────────────────────────┘
```

Runtime never replicates engine logic. Every BPMN-semantic operation routes through `pkg/thresher.Thresher` (which routes to `internal/instance/Instance` per ADR-001).

### 4.2 Runtime non-responsibilities

What the runtime does NOT do (and why):

| Concern | Why not in runtime |
|---|---|
| BPMN execution semantics | Owned by `internal/instance/` per ADR-001 |
| State persistence | `pkg/repository.Repository` adapter; runtime just selects and wires which adapter |
| Authorization decisions | `pkg/auth.AuthorizationProvider` per ADR-002; runtime supplies actor + action + resource context |
| Message correlation | `internal/instance/Instance` per [bpmn-spec/semantics/correlation.md](../bpmn-spec/semantics/correlation.md) |
| Expression evaluation | `pkg/model/expression.ExpressionEngine` per ADR-003 |
| Tenancy isolation enforcement | Repository adapter enforces query-level filtering; runtime only propagates the tenant ID |

If a future requirement seems to belong in runtime but actually requires reaching into engine internals, it's a signal that the engine's extension surface needs an addition — not that runtime should bypass the contract.

### 4.3 Lifecycle: explicit phased startup

```
gobpm-server main()
  ├─ Phase 1 — Bootstrap
  │    Load config (YAML + env-var expansion)
  │    Validate config
  │    Construct Logger adapter; wire as engine's Logger
  │
  ├─ Phase 2 — Observability stack
  │    Construct Tracer adapter (OTel by default)
  │    Construct MetricsRecorder adapter (OTel/Prometheus)
  │    Subsequent startup failures emit traces and metrics
  │
  ├─ Phase 3 — Data plane adapters
  │    Construct Repository adapter
  │    Construct MessageBroker adapter
  │    Construct Clock adapter
  │    Construct ExpressionEngine adapter
  │    Construct AuthorizationProvider adapter
  │    Run adapter.Start() for each that implements Starter (per ADR-002 §8.3)
  │
  ├─ Phase 4 — Task plane adapters
  │    Construct WorkerDispatcher adapter (per SAD-001 §13.2)
  │    Construct TaskDistributor adapter
  │    Run their Start() methods
  │
  ├─ Phase 5 — Engine
  │    Construct thresher.Thresher with all wired adapters
  │    thresher.Run(rootCtx)
  │
  ├─ Phase 6 — API surfaces
  │    Construct HTTP server with middleware chain
  │    Construct gRPC server with interceptor chain
  │    Begin listening on configured addresses
  │    Mark /readyz as ready
  │
  └─ Phase 7 — Signal handler
       Wait for SIGTERM / SIGINT
       Initiate graceful shutdown (see §4.4)
```

If any phase fails, runtime aborts startup, calls Stop() on already-started adapters in reverse order, and exits with non-zero status. The Logger from Phase 1 ensures failure context is captured.

### 4.4 Graceful shutdown

On SIGTERM / SIGINT:

```
1. Mark /readyz as NOT ready (load balancer stops routing new requests)
2. Stop accepting new HTTP / gRPC connections (graceful close)
3. Wait for in-flight requests to drain (configurable timeout, default 30s)
4. Call thresher.Thresher's Stop / cancel — engine drains in-flight Instances
   per ADR-001 v.3 §4.6 context cancellation cascade
5. Run adapter.Stop() for each Stopper adapter in REVERSE startup order
6. Close Logger / Tracer / MetricsRecorder (final flush)
7. Exit 0
```

If the drain timeout exceeds, runtime force-cancels remaining work (cascade through ctx). Outstanding state is in `Repository`; on restart, rehydration (the Persistence & State ADR; runtime invariants in ADR-001 v.3 §4.7) picks up where things left off.

### 4.5 API surface — service groups

The API surface is organized into logical **service groups** rather than a flat list of endpoints. Each group is delivered over REST, gRPC, or both — the exact transport-per-group is an implementation decision pinned in each group's SRD.

| Service group | Purpose | Likely transports (subject to SRD) |
|---|---|---|
| **Process registry** | Register / list / unregister Process definitions | REST (likely); gRPC parity TBD |
| **Instance lifecycle** | Start / get / terminate Process Instances; list active instances | REST + gRPC (both transports of interest — REST for casual clients, gRPC for high-throughput callers) |
| **User task** | Claim / complete / delegate UserTasks; list tasks visible to the actor | REST (likely); gRPC parity TBD |
| **Diagnostic** | Instance state inspection, token positions, history queries, manual intervention (move-token, retry, force-terminate) | REST + gRPC both — diagnostics serve operators (REST) and automated tooling (gRPC) |
| **Event streaming** | Server-streaming TokenEvents from instances; audit-class event feed for compliance pipelines | gRPC primary (server-streaming RPCs); REST via Server-Sent-Events as a fallback if needed |
| **Worker dispatch** | Worker registration + task-execution dispatch per SAD-001 §13.2 | gRPC primary (bidirectional streaming, mTLS); REST fallback may be added |
| **Health & ops** | Liveness, readiness, profiling, metrics scrape | HTTP only (standard endpoints expected by load balancers, Prometheus, k8s probes) |

Illustrative examples (not prescriptive):

- A Process-start operation might be `POST /api/v1/processes/{id}/instances` (REST) or `rpc StartInstance(StartRequest) returns (Instance)` (gRPC). Both could exist at parity.
- A TokenEvent stream is naturally a gRPC server-streaming RPC: `rpc StreamTokenEvents(Filter) returns (stream TokenEvent)`.

Each group's concrete endpoint shapes, request/response schemas, gRPC service definitions, and naming conventions are pinned in that group's implementation SRD. This ADR defines the groups and their security/lifecycle contracts; SRDs define their shapes.

### 4.6 Tenancy propagation

Tenant ID flows via `context.Context` from request entry to engine call. Key invariant: **every engine operation that reads or writes per-tenant state happens under a `context.Context` that carries the tenant ID.**

```go
// Runtime — HTTP middleware
func tenantMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        tenantID := extractTenantID(r)              // from header, JWT claim, or subdomain
        if tenantID == "" {
            // policy: reject vs anonymous-tenant vs default-tenant
            // configurable per deployment
        }
        ctx := tenancy.WithTenantID(r.Context(), tenantID)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

// Core — Repository adapter
func (r *postgresRepository) ListInFlight(ctx context.Context) ([]InstanceID, error) {
    tenantID := tenancy.TenantIDFromContext(ctx)
    rows, err := r.db.QueryContext(ctx,
        "SELECT id FROM instances WHERE tenant_id = $1 AND state = 'active'",
        tenantID)
    // ...
}
```

Cross-tenant operations (an actor with multi-tenant privileges querying across tenants) are NOT supported in v.1. Future feature, requires a separate ADR.

**Tenancy on engine-internal operations** (timer firing, signal delivery, message correlation): these happen on the engine's own internal context. The internal context carries the tenant ID derived from the affected Instance's persisted tenant ID. There is no "ambient" engine context that has no tenant.

### 4.7 AuthN provider model

Runtime defines an `AuthN` interface — a provider extracts an identity from an incoming request and returns it (or nil for anonymous, or error for explicit auth failure). The extracted identity flows via `context.Context` into engine operations.

Three properties of the AuthN architecture:

1. **Multiple providers active simultaneously.** A deployment can wire OIDC + mTLS + service tokens at the same time. Runtime's middleware/interceptor chain consults the providers eligible for the inbound request's service group.

2. **Per-service-group selection.** Each service group (§4.5) declares which AuthN provider categories are acceptable for it. Examples:
   - Human-facing groups (Process registry, Instance lifecycle, User task) typically accept OIDC / JWT.
   - Service-to-service groups (Worker dispatch, Event streaming consumers, Audit consumers) typically accept mTLS or service tokens.
   - Diagnostic group typically accepts an elevated-permission token category.
   - Health & ops are typically unauthenticated (probes from load balancers / k8s).
   The mapping is configurable per deployment; the table above is a default.

3. **Module-registered providers.** Adapter modules MAY register their own AuthN providers via a runtime registration interface (similar in shape to `Starter` / `Stopper` per ADR-002 §8.3 — type assertion + registry call during Phase 2 startup). Examples a future adapter could supply: `vault-token`, `corp-sso`, `hmac-signed-request`, `nonce-replay-protection`. Deployments configure which registered providers apply to which service group.

Provider categories likely shipped with runtime (each its own adapter module per ADR-003 §4.6):

- OIDC / OAuth2 (authorization-code flow + id-token validation)
- JWT bearer (validation against a JWKS endpoint)
- mTLS (client certificate verification, fingerprint-as-identity)
- Service tokens (opaque tokens issued by runtime or external service)
- Basic auth — development only

Identity payload carries actor ID, groups (for routing UserTasks per BPMN spec), tenant ID (when the provider also conveys tenancy), and a pass-through attribute map for AuthZ context.

The extracted identity flows into `context.Context`. Engine sees it via `pkg/auth.AuthorizationProvider.Authorize(ctx, action, resource)` per ADR-002 §4.2. Concrete interface shapes, identity struct field names, and registration API are SRD-level per the AuthN-implementation SRD.

### 4.8 AuthZ context propagation

Per ADR-002 §4.2, `AuthorizationProvider` is the extension point at the core level. Runtime ensures the context handed to core operations carries:
- The actor's Identity (per §4.7)
- The tenant ID (per §4.6)
- Trace span (for audit linkage)

Engine calls AuthZ at sensitive points (start Process, claim UserTask, cancel Instance, manual move-token, etc.). The AuthorizationProvider receives the full ctx + action description + resource description and returns allow/deny + decision reason (logged for audit).

Decision caching, fail-closed semantics, and decision metrics per ADR-002 §8.2 are responsibilities of the `AuthorizationProvider` adapter implementation.

### 4.9 Observability stack wiring

Runtime selects and constructs adapter implementations of core's observability interfaces:

| Core interface | Runtime adapter source |
|---|---|
| `pkg/observability.Logger` | `adapters/observability-otel-slog/` (slog → OTel logs); or plain `pkg/observability/slog/` for the default |
| `pkg/observability.Tracer` | `adapters/observability-otel/` (OTel SDK) |
| `pkg/observability.MetricsRecorder` | `adapters/observability-otel/` OR `adapters/observability-prometheus/` |
| `pkg/clock.Clock` | `pkg/clock/syscl/` (default; tests inject fake) |

Runtime additionally configures the OTel exporter targets (collector endpoint, sampling rate, resource attributes per OTel resource semantic conventions: `service.name=gobpm-server`, `service.version=v0.x.y`, `service.instance.id=<pod-id>`).

Per ADR-002 §8.1, runtime sets engine-level attributes (`gobpm.engine_id`) once at startup; engine adds per-instance/per-track attributes during execution.

### 4.10 Diagnostic and operational endpoints

The Diagnostic service group exposes operations for post-incident diagnosis and intentional intervention. Three operation categories, each with its own AuthZ class:

| Category | Examples (illustrative) | AuthZ class |
|---|---|---|
| **Read-only inspection** | Instance state snapshot; token positions; history queries; stuck-token / deadlock detection; in-flight stats | `instance:read` / `engine:read` |
| **Admin intervention** | Manual token move; activity retry; force-terminate instance; cancel running tracks | `instance:admin` |
| **Cluster ops** | (future, per ADR-008) instance migration between nodes; force-failover; cluster-wide drain | `engine:admin` |

These operations route through `pkg/thresher` methods (where they exist) or via engine façade methods that mediate access to `internal/instance/` per ADR-001. Specific endpoint paths, methods, and request/response shapes are SRD-level per the Diagnostic-group implementation SRD.

### 4.11 Health-check endpoints

Two probes, mirroring the Kubernetes-standard liveness/readiness distinction:

- **Liveness** — the runtime process is alive and the engine event loop hasn't died. Cheap (atomic-flag check). Used by orchestrators to decide whether to restart the container.
- **Readiness** — engine AND all adapters' `HealthChecker`s (per ADR-002 §8.3) pass. Sub-second to evaluate (iterates adapters). Used by load balancers to decide whether to route requests.

Readiness flips to "not ready" immediately on shutdown signal — before the actual drain begins — so load balancers stop sending new requests within their probe interval.

Specific endpoint paths follow ops conventions (likely `/healthz` and `/readyz` per Kubernetes idiom); pinned in the Health & ops SRD.

### 4.12 Configuration model

Hierarchical structured config with one section per concern (engine, observability, repository, message broker, AuthN providers + per-group selection, AuthZ, worker dispatcher, HTTP/gRPC listeners, shutdown, …). Environment-variable expansion (`${VAR}`) for secrets.

The schema shape (one section per concern, `type:` field selecting which adapter variant is wired, adapter-specific sub-keys for that adapter's needs) is the conceptual contract. The exact format (YAML / TOML / other), library choice for loading, and concrete schema are pinned in the Configuration SRD.

Defaults are filled in for omitted values; required values without defaults cause a clear startup failure with a structured error message naming what's missing.

Secrets MUST NOT live in the config file itself — only env-var references. The Configuration SRD pins the secret-source list (env vars, secret-store integrations).

## 5. Conception vs Current Code — Deliberate Departures

| Topic | Current code | This ADR | Required change |
|---|---|---|---|
| Runtime layer | Doesn't exist | A full `runtime/` submodule per §4.1 | Per ADR-003 §4.6 step 1 (`runtime/` scaffold) + the substantial implementation work this ADR describes (multi-SRD scope; runtime is its own product). |
| Multi-tenancy | Not modeled in core | Tenant ID via `context.Context`; Repository enforces query-level scoping; runtime extracts and propagates | New `runtime/tenancy/` package; Repository adapter updates to honor tenant from ctx. |
| AuthN | Not present | `runtime/auth/` AuthN interface + adapter modules (oidc, jwt, mtls, basic) | All net-new. Each adapter is its own module per ADR-003 §4.6. |
| API surface | None | REST endpoints + gRPC services per §4.5 | All net-new. Protobuf schemas under `runtime/proto/`. |
| Health checks | None | `/healthz`, `/readyz` integrated with adapter `HealthChecker` | All net-new. ADR-002 §8.3 already defined the `HealthChecker` extension interface. |
| Configuration | Not formalized | YAML config with env-var expansion per §4.12 | All net-new. Suggested library: `koanf` or `viper` (decision per implementation SRD). |
| Lifecycle ordering | Not formalized | 7-phase startup + reverse shutdown per §4.3-4.4 | All net-new. Maps to ADR-002 §8.3 `Starter` / `Stopper` interfaces. |

Each row is its own SRD-class implementation. The runtime is substantial — likely 6–10 distinct SRDs to build out.

## 6. Consequences

### 6.1 Pros

- **Clear separation**: `gobpm` core stays embeddable; `gobpm-runtime` adds the standalone-server story without polluting core.
- **Adapter-driven AuthN/Z**: corporate identity providers integrate without modifying core code.
- **Operations-ready**: health checks, observability, diagnostics, profiling — all standard production needs covered from day one.
- **Tenancy-aware without overhead** for single-tenant users (tenant ID is just an empty string in their config).
- **Standard Go stack**: `net/http`, `grpc-go`, YAML config — no surprising dependencies, no DSL inventions.

### 6.2 Cons

- **Substantial implementation surface**: this ADR describes a complete production server. v.1 implementation is multi-SRD work; runtime won't be "done" in a single sprint.
- **Two API surfaces (REST + gRPC)**: more maintenance than one. Mitigated by gRPC being scoped to streaming-only.
- **Adapter dependencies in runtime**: runtime imports many adapter modules; binary size grows with adapter count. Mitigated by build flags / conditional compilation if a deployment wants a minimal binary.
- **Configuration model surface**: every adapter has its own config schema. Mitigated by per-adapter README documenting its YAML fragment.

### 6.3 Implications for adjacent decisions

- **ADR-001 Execution Model**: Runtime invokes engine operations via `pkg/thresher`; engine's `context.Context` cancellation cascade is the underlying shutdown mechanism.
- **ADR-002 Extension Architecture**: Runtime wires adapter implementations of every core extension interface; the `Starter` / `Stopper` / `HealthChecker` optional interfaces from §8.3 are first-class consumed here.
- **ADR-003 Module Layout**: Runtime is the `runtime/` submodule; adapter authn implementations are under `adapters/` per the module-layout rules.
- **Future ADR-008 Distribution & Scale**: Will extend §4.5's `WorkerDispatchService` to a full clustered dispatch model. Multi-node coordination, cluster-wide signal broadcast, and instance pinning live there.

## 7. Verification

| What | How |
|---|---|
| **Phased startup ordering** | Integration test: construct runtime; observe order of adapter.Start() calls via mocks; assert order matches §4.3. |
| **Phased shutdown ordering** | Integration test: send SIGTERM; observe order of adapter.Stop() calls; assert reverse-startup order. |
| **Graceful drain respects timeout** | Test: in-flight request takes 10s; SIGTERM with drain_timeout=5s; assert force-cancellation kicks in at 5s. |
| **Tenancy propagation** | E2E test: HTTP request with X-Tenant-ID header → POST to start Process → assert resulting Instance in Repository has correct tenant_id. |
| **Cross-tenant isolation** | E2E test: tenant A's request cannot read tenant B's instance via GET /api/v1/instances/{id}. Assert 403/404. |
| **AuthN provider chain** | Test: configure OIDC + mTLS; request with valid mTLS cert → identity extracted via mTLS provider; request with valid OIDC token → identity via OIDC. |
| **AuthZ deny logged** | Test: deny decision → INFO log line with structured attributes (actor, action, resource, decision=deny, reason). |
| **`/readyz` reflects adapter health** | Test: simulate failing Repository HealthChecker; assert `/readyz` returns 503 until restored. |
| **`/readyz` flips to not-ready on shutdown signal** | Test: SIGTERM → `/readyz` immediately returns 503 before drain begins. |
| **Diagnostic dumps don't leak across tenants** | Test: tenant A's dump request for tenant B's instance returns 403/404. |
| **Engine semantics untouched by runtime** | Verification: code review confirms runtime methods never duplicate BPMN-semantic logic. Static check optional. |

**Acceptance gate** (Draft → Accepted): the verification tests above MUST exist and pass against the runtime implementation. Until then the ADR remains Draft.

## 8. Enterprise-Readiness Recommendations

### 8.1 API versioning

All HTTP endpoints under `/api/v1/`. When v2 is needed, both run in parallel for at least one minor version. Deprecation header (`Deprecation: <date>`) on v1 endpoints during the parallel period.

gRPC services use protobuf package versioning: `gobpm.v1.WorkerDispatchService`; v2 lives in `gobpm.v2.*`.

This is standard but worth pinning early — retrofitting versioning after the first client integrates is painful.

### 8.2 Rate limiting and quotas

Runtime SHOULD support per-tenant rate limiting on:
- Process instance starts per minute
- UserTask claims per minute
- Diagnostic dump requests per minute (these are expensive)

Default limits should be conservative; ops adjusts per deployment. Limits prevent one tenant from starving others on a shared deployment.

### 8.3 Audit log shipping

The AuditStreamService (per §4.5) gRPC stream is the primary audit feed. Operations SHOULD configure an external audit-pipeline consumer (Kafka, Elastic, Splunk) subscribing to this stream and writing to durable storage outside the gobpm deployment.

Per ADR-002 §8.5, audit events MUST be durable; runtime SHOULD log a metric (`gobpm_audit_stream_lag_seconds`) reflecting time between event generation and consumer ack so ops can detect slow / failed consumers.

### 8.4 Secret handling

YAML config supports `${VAR}` env-var expansion. Secrets (DB passwords, OIDC client secrets, TLS keys) MUST come from environment variables OR a secret-store integration, NEVER from the YAML file directly.

Recommended secret-store adapters (future): HashiCorp Vault, AWS Secrets Manager, Kubernetes Secrets. Until those exist, env-var-from-secret is the supported path.

### 8.5 TLS for both HTTP and gRPC

Production deployments MUST terminate TLS at the runtime listener (or at a sidecar proxy):
- HTTP: TLS configured via the YAML `http.tls:` block (cert/key paths).
- gRPC: TLS configured via `grpc.tls:` block; mTLS for service-to-service when worker dispatch is over gRPC.

A "plaintext only on localhost" mode is acceptable for development; non-localhost listeners SHOULD refuse to start without TLS configuration unless an explicit `--insecure` flag is passed.

### 8.6 Per-tenant resource accounting

For multi-tenant deployments, runtime SHOULD emit per-tenant metrics:
- `gobpm_tenant_instances_active{tenant_id="..."}` gauge
- `gobpm_tenant_requests_total{tenant_id="...", endpoint="..."}` counter

Cardinality risk: in deployments with thousands of tenants, per-tenant labels explode metrics storage. Cardinality cap configurable via runtime config; over-cap tenants get aggregated into a `tenant_id="_other"` bucket.

### 8.7 Backpressure on the worker-dispatch gRPC stream

The `WorkerDispatchService` is a streaming bidirectional protocol. Runtime MUST apply backpressure when a worker can't keep up — pause dispatch to that worker, optionally re-dispatch the in-flight work to another available worker.

Heartbeat protocol per ADR-002 §8.2: worker sends heartbeat every N seconds; runtime cancels in-flight work and removes worker from dispatch pool if heartbeat is missed beyond threshold.

### 8.8 Observability completeness

Every API endpoint SHOULD emit:
- Logger: structured INFO log with `gobpm.tenant_id`, `gobpm.actor_id`, `endpoint`, `status_code`, `duration_ms`
- Tracer: span covering the handler with attributes matching §4.9 OTel conventions
- MetricsRecorder: counter `gobpm_http_requests_total{endpoint, status_code, tenant_id}`; histogram `gobpm_http_request_duration_seconds{endpoint}`

Skipping this for "simple" endpoints in early development creates the painful "we have no idea what happened in production" situation. Standardize from day one.

## 9. References

- [SAD-001 Vision & Architecture](SAD-001-vision-and-architecture.md) — §12 Runtime Environment (this ADR refines); §13 Distribution & Scale (preliminary — future ADR-008 will extend §4.5 worker-dispatch); §9 Module Layout (defines `runtime/` submodule)
- [ADR-001 Execution Model](ADR-001-execution-model.md) — `context.Context` cancellation cascade that this ADR's §4.4 graceful shutdown uses
- [ADR-002 Extension Architecture](ADR-002-extension-architecture.md) — extension interfaces this ADR wires; §8.3 `Starter` / `Stopper` / `HealthChecker` first-class consumers; §8.1 observability attribute conventions
- [ADR-003 Module Layout](ADR-003-module-layout.md) — `runtime/` submodule placement, adapter module conventions, import-direction rules
- [docs/bpmn-spec/semantics/correlation.md](../bpmn-spec/semantics/correlation.md) — message correlation; tenancy-bounded
- AuthN/Z model — the IAM concern is covered by §4.7 (AuthN provider model) here plus the `AuthorizationProvider` extension in [ADR-002 v.1](ADR-002-extension-architecture.md); a dedicated AuthN/Z ADR will be authored as part of that work. (Supersedes the earlier standalone IAM ADR, removed in `8855f1d` with its content absorbed into the design docs.)

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-05-31 | Ruslan Gabitov | Initial Draft. Pre-acceptance iteration ongoing; amendments folded into this Draft without per-round history rows (per project doc-history discipline). When v.1 flips to Accepted, this row records the Accepted state. |
