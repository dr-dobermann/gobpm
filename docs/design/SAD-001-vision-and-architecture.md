# SAD-001 — goBpm Vision & Architecture

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-05-29 |
| Owner | Ruslan Gabitov |
| Supersedes | — |
| Conformance scope | [docs/bpmn-spec/conformance.md](../bpmn-spec/conformance.md) |

## 1. Purpose

This document is the top-level architectural definition of **goBpm**. It establishes vision, scope, system boundaries, principles, module layout, and references to subordinate ADRs that crystallize specific decisions. Every ADR in this project refers up to this SAD; this SAD is the single coherent picture of "what we are building and why."

It is **not** an implementation plan. Implementation specifics (per-feature requirements, migration steps, deployment topology) live in SRDs and FIXes that reference this document.

### 1.1 Document classes used in this project

| Class | Purpose | Lifecycle |
|---|---|---|
| **SAD** | Top-level architecture; vision + system boundaries + principles | Concept-level. Versioned. Evolves over time as the architecture matures. |
| **ADR** | Architecture Decision Record — one concrete decision (execution model, module layout, ...) | Concept-level. Versioned. Each version is the current contract for that decision. |
| **SRD** | Software Requirements Document — what a specific landing must deliver | Implementation-level. One document per landing. Not retroactively edited after the landing — it is the historical snapshot of intent. |
| **FIX** | Fix design document — root cause + solution for a bug landing | Implementation-level. One document per fix landing. Same single-shot discipline as SRD. |

SAD and ADR are the right places for the kind of content this document carries. SRDs and FIXes will accompany specific implementation landings and reference back to whichever SAD/ADRs they implement.

## 2. Vision

> **goBpm is a native Go BPMN 2.0 engine designed to embed directly into Go applications as a minimal, robust library — and to scale up to a standalone process server through additive runtime components, without forcing users to ship dependencies they do not need.**

Two distinct user journeys are first-class:

1. **Embedded library use.** A Go developer imports `github.com/dr-dobermann/gobpm`, constructs an engine with `thresher.New(id)` (zero options applies all defaults), registers a process, runs it. No external services required. The engine lives in the same process as the host application.

2. **Standalone runtime use.** An operator deploys a `gobpm-server` binary that exposes the engine over HTTP/gRPC, persists state to a real database, integrates with the organization's identity provider, and emits OpenTelemetry traces and Prometheus metrics. The runtime is built on the library — it is not a fork or a parallel implementation.

Both journeys MUST work with high quality. The library MUST NOT carry runtime baggage; the runtime MUST NOT reimplement the engine.

## 3. Goals

| # | Goal | Rationale |
|---|---|---|
| G1 | **BPMN 2.0 Process Execution Conformance + ComplexGateway extension** | Standard compliance is the product's reason to exist. See [docs/bpmn-spec/conformance.md](../bpmn-spec/conformance.md). |
| G2 | **Minimal core library: zero non-stdlib runtime dependencies in the engine hot path** | Embeddability requires not forcing transitive deps onto host applications. |
| G3 | **Out-of-the-box usability** | Zero-option `thresher.New(id)` produces a working engine with no wiring (defaults are the default). New users get a working example in <20 lines. |
| G4 | **Extensibility at every infrastructure concern** | Persistence, events, security, observability, expressions, human-task distribution, timers, message correlation backends — all behind interfaces. |
| G5 | **Predictable execution model** | Single event-loop goroutine per Process instance owns state; each track (thread of execution) runs in its own goroutine; the token is a projection of a track's position; `context.Context` is the cancellation contract. |
| G6 | **Production runtime as additive overlay** | Multitenancy, AuthN/Z, diagnostics, profiling, HTTP/gRPC APIs live in a separate module. Library users pay no cost for them. |
| G7 | **Solo-developer maintainability** | Code organized for incremental development. Multi-module monorepo over multi-repo split. Clear vertical slices over horizontal layers. |
| G8 | **Observable by default, not by accident** | Every state transition emits structured events; default observability is no-op; production observability is plug-in. |

## 4. Non-Goals

| # | Non-Goal | Reason |
|---|---|---|
| N1 | BPMN modeler / diagram editor | Out of project scope. Users author BPMN externally (Camunda Modeler, bpmn.io). |
| N2 | DMN engine (decision tables) | Distinct standard. May integrate via `BusinessRuleTask` calling an external DMN engine. |
| N3 | Choreography execution | Separate conformance subclass; excluded per [docs/bpmn-spec/conformance.md](../bpmn-spec/conformance.md). |
| N4 | Collaboration metamodel execution | `Pool`, `Participant`, `MessageFlow` at the Collaboration level are out of execution conformance. Inter-process messaging covered by Message events. |
| N5 | Diagram Interchange (DI/DC) | Visual layout metamodel. Not part of execution conformance. |
| N6 | BPEL mapping | Separate conformance subclass; not pursued. |
| N7 | BPMN XML parser, as a core library concern | The parser will exist (it has to, for adoption), but it is a separate module that constructs the in-memory model the engine consumes. Core library accepts pre-built models. |

> **Note on distribution / clustering.** Distribution is NOT a non-goal — see §13 Distribution & Scale. Task-level remote execution (ServiceTask / GlobalTask on external workers via an engine-owned fetch-and-lock job queue, ADR-021) and instance-level distribution (sticky routing per instance ID with persistence-based failover) are within the architectural envelope as additive overlays on the single-process foundation. Cluster-wide shared state (cross-node correlation, signal broadcast, shared variables) is an open question, addressable via DB-backed Repository + event broadcast — to be tackled when concrete demand materializes.

## 5. Stakeholders & Use Cases

| Stakeholder | Primary use case | Critical needs |
|---|---|---|
| **Embedded library user** (Go application developer) | Embed a BPMN engine in a Go application that already has its own HTTP, persistence, observability | Minimal deps; clean API; works without external services in tests; sensible defaults |
| **Runtime operator** | Deploy `gobpm-server` as a standalone BPMN service for the organization | Production persistence, multitenancy, AuthN/Z, observability, diagnostics, HA-readiness |
| **Extension developer** | Write a custom Repository / Authorization / Tracer / Expression adapter | Small, stable, well-documented interfaces; conformance contracts for each |
| **BPMN modeler** | Author BPMN 2.0 XML to be executed by goBpm | Strict spec conformance; clear feedback on unsupported elements; predictable runtime semantics |
| **Process owner / business user** | Observe, diagnose, intervene on running instances | Diagnostic API; history; instance state inspection; manual intervention (move token, retry, terminate) |

## 6. Quality Attributes

Priority levels: **P0** = mandatory for v.1.0; **P1** = required before public release; **P2** = nice-to-have, tracked.

| Attribute | Priority | Tactic |
|---|---|---|
| BPMN conformance | P0 | Conformance test suite (MIWG fixtures + project-internal); KB at [docs/bpmn-spec/](../bpmn-spec/) as normative reference; each implemented element cross-checked against KB |
| Robustness | P0 | Goroutine-leak-free architecture; `context.Context` cancellation cascade; no token spawned for long-wait states (rehydration model); deadlock detection for ComplexGateway |
| Minimal core deps | P0 | `core` go.mod limited to stdlib + `github.com/google/uuid` (already in use). All other deps live in adapter or runtime modules. |
| Out-of-the-box usability | P0 | Zero-option `thresher.New(id)` constructor (applies all defaults) + working example under 20 lines |
| Extensibility | P1 | Functional option per extension: `Repository`, `ExpressionEngine`, `WorkerDispatcher`, `MessageBroker`, `Clock`, `Logger`, `Tracer`, `MetricsRecorder`, `AuthorizationProvider` (the `TaskDistributor` human-routing interface is deferred to a dedicated human-interaction ADR) |
| Testability | P1 | All extension interfaces mockable (mockery); execution tests don't require external services; deterministic clock injection |
| Observability | P1 | Every state transition (per BPMN lifecycle) emits a typed event. **Default policy: visible-by-default, silenceable on opt-out.** `Logger` defaults to `slog.Default()` so production deployments don't accidentally lose telemetry. `Tracer` and `MetricsRecorder` default to no-op only because Go stdlib has no sensible default for them (OpenTelemetry adapter ships separately). Users who want less noise opt out explicitly by passing a discarding logger (`thresher.WithLogger(...)`). |
| Documentation | P1 | This SAD + ADRs + bpmn-spec KB + per-element reference + examples + runtime operator guide |
| Security | P2 | Authz hook points in core (sensitive operations defined); AuthN provider model; no built-in policy engine (delegated to runtime / adapter) |
| Performance | P2 | Goroutine-per-token gives natural parallelism; benchmarks track per-element latency; no early optimization beyond avoiding obvious sinks (no map-allocation in hot paths) |
| Distribution | P2 | Quickstart Docker image bundles runtime with sane defaults; single static binary for embedded `gobpm-server` use |

## 7. System Context

```
                                    ┌──────────────────────────┐
                                    │     BPMN 2.0 model       │
                                    │  (in-memory Go objects   │
                                    │   or parsed BPMN XML)    │
                                    └────────────┬─────────────┘
                                                 │ registers
                                                 v
   ┌──────────────────────────────────────────────────────────────────┐
   │                       goBpm core library                         │
   │                                                                  │
   │   Engine ─── Snapshot ─── Orchestrator ─── Tokens                │
   │      │                                                           │
   │      ├── Extension interfaces (Repository, EventHub, Logger,     │
   │      │    Tracer, MetricsRecorder, ExpressionEngine, ...)        │
   │      │                                                           │
   │      └── Default in-memory / no-op implementations               │
   └────────────┬──────────────────────────────────┬──────────────────┘
                │ imported by                      │ imported by
                v                                  v
   ┌─────────────────────────────┐    ┌─────────────────────────────┐
   │     Host Go application      │    │       gobpm-runtime         │
   │     (embedded library use)   │    │   ┌────────────────────┐    │
   │                              │    │   │  HTTP / gRPC API   │    │
   │   import gobpm               │    │   │  Tenancy           │    │
   │   engine := thresher.New(id) │    │   │  AuthN / AuthZ     │    │
   │   engine.Run(ctx)            │    │   │  Diagnostics       │    │
   │                              │    │   │  Profiling         │    │
   └─────────────────────────────┘    │   │  Observability     │    │
                                       │   └────────────────────┘    │
                                       └────────────┬────────────────┘
                                                    │ imports
                                                    v
                                       ┌─────────────────────────────┐
                                       │   Adapter modules           │
                                       │   (postgres, otel, oidc,    │
                                       │    casbin, redis-broker,    │
                                       │    ...)                     │
                                       └─────────────────────────────┘
```

Dependency direction is **always from outside in**: runtime imports core; adapters provide implementations of core interfaces; host applications import core directly. Core depends on nothing outside its own module.

## 8. Architecture Overview

### 8.1 Layer model (within `core`)

```
┌─────────────────────────────────────────────────────────────────┐
│  Public API           thresher.Thresher, thresher.New(...),     │
│  pkg/                 pkg/model/* (BPMN element constructors),  │
│                       extension interfaces                       │
├─────────────────────────────────────────────────────────────────┤
│  Instance lifecycle   internal/instance/, internal/runner/,      │
│                       internal/exec/  — Orchestrator + Token     │
├─────────────────────────────────────────────────────────────────┤
│  Event processing     internal/eventproc/  — EventHub, waiters   │
├─────────────────────────────────────────────────────────────────┤
│  Scope                internal/scope/  — hierarchical data       │
├─────────────────────────────────────────────────────────────────┤
│  Snapshot             internal/instance/snapshot/  — immutable   │
│                       process definitions, the execution input   │
├─────────────────────────────────────────────────────────────────┤
│  Model                pkg/model/  — BPMN element types           │
│                       (Activity, Event, Gateway, Flow, Data, ...) │
└─────────────────────────────────────────────────────────────────┘
```

Dependencies flow **downward only**. Higher layers depend on lower; lower layers know nothing of higher.

### 8.2 Key responsibilities

| Component | Responsibility |
|---|---|
| **Engine (`thresher`)** | Top-level façade. Holds extension implementations. Manages Process registry and Process Instance lifecycle. The name `thresher` (current `pkg/thresher`) is retained — it is the project's identity for "the BPM engine." |
| **Snapshot** | Immutable, validated representation of a Process definition. Engine accepts a Snapshot, not a mutable model. |
| **Orchestrator** | One goroutine per Process Instance. Owns instance state. Receives `TokenEvent`s from tokens, applies state transitions, spawns new tokens, persists checkpoints. |
| **Token** | One goroutine per active token. Executes the element under the token (Task, Gateway evaluation, Event wait). Communicates back to Orchestrator via channel. |
| **EventHub** | Internal event distribution. Routes Message / Signal / Timer / Conditional triggers to subscribed waiters across instances. |
| **Scope** | Hierarchical data context. Resolves DataObject visibility, Property scoping, correlation key scope. |
| **Repository** (interface) | Persists Process Instance state, history, message inbox. Default: in-memory. |
| **All other extension interfaces** | Allow injection of `Logger`, `Tracer`, `MetricsRecorder`, `ExpressionEngine`, `WorkerDispatcher`, `MessageBroker`, `Clock`, `AuthorizationProvider`. |

Detailed execution semantics: **ADR-001 Execution Model**. Detailed extension model: **ADR-002 Extension Architecture**.

## 9. Module Layout

Multi-module monorepo. Each subdirectory listed with its own `go.mod` versions independently and isolates its dependency tree from sibling modules.

```
github.com/dr-dobermann/gobpm/                           ← repo root
├── go.mod                                                ← core library (current state)
├── cmd/                                                  ← thin CLI entry points (current)
├── pkg/                                                  ← public API of core (current)
│   ├── model/                                            ← BPMN element types
│   ├── thresher/                                         ← engine façade (keep name `thresher`)
│   ├── errs/, set/                                       ← utilities
│   └── (future: extension interfaces — repository, observer, ...)
├── internal/                                             ← core internals (current)
│   ├── instance/, runner/, exec/, eventproc/,
│   │   scope/, interactor/, renv/
├── examples/                                             ← multi-module already
│   ├── basic-process/   (own go.mod)
│   ├── simple-timer/    (own go.mod)
│   └── timer-event/     (own go.mod)
├── runtime/                                              ← FUTURE — gobpm-server (own go.mod)
│   ├── server/            HTTP / gRPC API
│   ├── tenancy/           multi-tenant context propagation
│   ├── auth/              AuthN + AuthZ glue
│   ├── obs/               observability wiring
│   ├── diag/              diagnostic endpoints
│   └── cmd/gobpm-server/  the runnable binary
├── adapters/                                             ← FUTURE — each own go.mod
│   ├── postgres/          Repository implementation
│   ├── otel/              Tracer + MetricsRecorder via OpenTelemetry
│   ├── oidc/              AuthN provider
│   ├── casbin/            AuthZ policy engine
│   └── redis-broker/      MessageBroker implementation
├── doc-source/                                           ← FUTURE — BPMN XML parser (own go.mod)
└── docs/                                                 ← shared documentation
    ├── design/            ← this directory
    ├── adr/, srd/
    ├── analytics/
    └── bpmn-spec/         ← normative BPMN 2.0 reference KB
```

### 9.1 Import direction rules

- **`core` (root module)** depends only on Go stdlib + `github.com/google/uuid`. Nothing else. No imports from `runtime/`, `adapters/`, `examples/`.
- **`runtime/`** imports `core` and selected `adapters/*` chosen by the operator. No imports from `examples/`.
- **`adapters/*`** each import `core` (to satisfy its interfaces) and the relevant third-party SDK (e.g., `lib/pq` for postgres adapter). No imports across adapters.
- **`examples/*`** each import `core` directly. They demonstrate library usage; they do NOT depend on `runtime/`.

### 9.2 Evolution — scaffold upfront

Today: only `core` and `examples/` exist as modules.

**Scaffold all target modules upfront**, even if they are initially empty placeholders. Rationale: establishing import-direction discipline (§9.1) on day 1 is much cheaper than retrofitting it later. An empty `runtime/go.mod` + a single `doc.go` documents the intent and reserves the boundary; the first real code lands without restructuring.

Concretely, the first pass establishes:

- `runtime/` with `go.mod` + `doc.go` + `cmd/gobpm-server/main.go` (stub: prints "not yet implemented")
- `adapters/` directory with at least one placeholder module (e.g., `adapters/memory/` for the default in-memory Repository extracted from core's reference impl)
- Cleanup of obsolete or misplaced files in `docs/` (excalidraw scratch files, stale README index, etc. — to be triaged before this SAD is accepted)
- Import-direction rules (§9.1) enforced in CI from day 1 (`go vet` + a `make lint-modules` target that fails on disallowed import edges)

Subsequent modules (`adapters/postgres/`, `adapters/otel/`, ...) are added when their first concrete consumer materializes — but always into the established structure, never via reorg.

### 9.3 Future option: split to separate repositories

If the monorepo becomes unwieldy (unlikely while solo-developed, possible at scale), the multi-module structure makes the split a directory-move operation:
- `runtime/` → `github.com/dr-dobermann/gobpm-runtime`
- `adapters/postgres/` → `github.com/dr-dobermann/gobpm-postgres`
- etc.

Detailed module layout decision and import rules: **ADR-003 Module Layout**.

## 10. Execution Model (overview)

Detailed in **ADR-001 v.3 Execution Model** (Accepted). Key points captured here for vision-level coherence:

- **One event-loop goroutine per Process Instance.** Owns the instance state. Single-threaded mutation — no locks on instance state.
- **One track goroutine per thread of execution.** A `track` carries its current flow position and executes the element there, reporting back via a typed event channel. The **token** is a *projection* of a track's current step (the BPMN control position), not a stored object or a goroutine of its own.
- **`context.Context` is the cancellation contract.** The instance owns the root context. Each track gets a derived context. Terminate End Event → cancel root context → all tracks see `ctx.Done()` → graceful exit.
- **Save / restore instance context is a P0 capability.** The engine MUST be able to checkpoint a Process Instance's full execution context to the `Repository` and reconstitute it later — into either the same runtime process (after a restart) or a different one (for migration / failover / distribution). Goroutines are the **execution medium**, persistence is the **state of record**.
- **Long waits do NOT hold goroutines.** A UserTask waiting 3 days externalizes state to `Repository`. The track goroutine exits. When the trigger arrives (human submits form, timer fires, message arrives), the instance rehydrates from persistence and spawns a fresh track. Combined or alternative mechanisms (event-driven wake-up, polling, push from external system) are all valid — the persistence + rehydration contract is invariant.
- **Persistence checkpoints align with lifecycle transitions.** State persisted at every observable BPMN state transition (per `docs/bpmn-spec/state-machines/activity-lifecycle.md`).
- **On runtime start / restart**, the runtime queries `Repository` for in-flight instances and rehydrates them. Recovery should be straightforward and bounded — not a fragile dance.
- **Instances are created by an explicit start *or* by an event.** Beyond `StartProcess`, a **message start event** or an instantiating `ReceiveTask` spawns an instance when a matching message arrives — the instance is *born from the event* (the start node pre-fired, its payload bound), created by a definition-level **instance-starter** (ADR-014/ADR-015). **Message correlation** (ADR-016) decides whether a message creates a new instance or routes to an existing one, by a composite key derived from the payload; a `WithManualStart` registration opts a process out of auto-instantiation (tests / back-pressure).

## 11. Extension Model (overview)

Detailed in **ADR-002 Extension Architecture**. Key points:

- Go-idiomatic: interfaces + functional options.
- Every infrastructure concern has a default implementation in core (no-op or in-memory) so a zero-option `thresher.New(id)` works.
- Production implementations live in `adapters/*` modules.
- Assembly: `thresher.New(id, thresher.WithRepository(r), thresher.WithLogger(l), thresher.WithTracer(t), ...)`.

Initial extension interface set (subject to refinement in ADR-002):

| Interface | Purpose | Default impl |
|---|---|---|
| `Repository` | Instance + history + inbox persistence | in-memory |
| `EventHub` | Event distribution (already in repo) | in-memory |
| `ExpressionEngine` | FormalExpression evaluation | Go-native expr eval |
| `TaskDistributor` | UserTask routing to humans | deferred — human-interaction ADR (current code ships `WorkerDispatcher` below, not this) |
| `WorkerDispatcher` | Asynchronous job queue (enqueue + fetch-and-lock + report) for ServiceTask / GlobalTask external workers (§13.2, ADR-021) | in-process (in-memory queue + local worker pool) |
| `MessageBroker` | Message correlation inbox | in-memory |
| `Clock` | Timer source (testability) | `time.Now` wrapper |
| `Logger` | Structured logging | `slog.Default()` — visible by default; pass a discarding logger via `thresher.WithLogger(...)` for low-noise environments |
| `Tracer` | Distributed tracing | no-op |
| `MetricsRecorder` | Counter / gauge / histogram emission | no-op |
| `AuthorizationProvider` | Authorization decision at sensitive ops | "allow all" |

## 12. Runtime Environment (overview)

Detailed in **ADR-004 Runtime Environment Contract**. Lives in `runtime/` submodule.

| Concern | Ownership | Notes |
|---|---|---|
| Multitenancy | Runtime, propagated to core via `context.Context` | Core accepts tenant-aware context, uses as scoping key for `Repository` lookups. Runtime enforces isolation policy. |
| AuthN | Runtime | Pluggable identity providers: OIDC, JWT, mTLS. Core does not authenticate. |
| AuthZ | Hook points in core; policy engine in runtime / adapter | Core defines sensitive operations (start process, claim user task, cancel instance, ...) and calls `AuthorizationProvider.Authorize(...)`. Default impl allows all. Production impl wired via adapter. |
| Observability | Hooks in core; wiring in runtime / adapter | Core emits via `Logger`, `Tracer`, `MetricsRecorder`. Runtime wires OpenTelemetry. |
| Diagnostics | Runtime | REST API: instance state dump, token positions, history query, manual intervention (move token, retry, terminate). |
| Profiling | Runtime + core hooks | Built-in `pprof` endpoint; per-element latency metrics; BPMN-specific stuck-token / deadlock alerts. |
| HTTP / gRPC API | Runtime | Public surface for non-Go clients. Maps engine operations to wire protocol. |

## 13. Distribution & Scale

> _**Status: preliminary, subject to refinement.** This section was sketched in the first review round but explicitly deferred for deeper discussion before SAD acceptance. The headline framing (additive overlay; an engine-owned async fetch-and-lock job queue per ADR-021; persistence as the foundation) is the working direction. The task-level worker-execution model is now decided in **ADR-021**; remaining specifics — remote protocol choice (ADR-004), cluster-wide state design — will be refined here or relocated to a dedicated ADR (ADR-008) before this SAD flips to Accepted._

Single-process execution is the foundation. Distribution is achieved as an **additive overlay** through extension points and runtime-level dispatching — never by rewriting the core orchestration model.

### 13.1 Levels of distribution

| Level | Mechanism | Status |
|---|---|---|
| **Single-instance, single-node** | Event-loop + track goroutines, all in one process (the foundation, §10) | Always supported |
| **Task-level remote execution** | Selected tasks (ServiceTask, GlobalTask) execute on external workers that **fetch-and-lock** jobs from an engine-owned asynchronous queue (ADR-021) | Extension point; in-process in-memory queue in 0.1.x (ADR-021), remote transport in ADR-004; see §13.2 |
| **Instance-level distribution** | Each Process Instance pinned to one runtime node via sticky routing (consistent hash on instance ID); failover via persistence rehydration (§10) | Feasible by design; deferred until multi-node deployment demand materializes |
| **Cluster-wide shared state** | Cross-node visibility of Signals / Message correlation / shared variables | Open question; solvable via DB-backed `Repository` + event broadcast + correlation-backend extension. To be addressed when concrete demand materializes. |

### 13.2 Task-level remote execution model

The runtime exposes an **asynchronous job queue** — the Camunda external-task model, decided in **ADR-021 Service Task Execution Model** (concrete remote protocol — HTTP long-poll, gRPC stream, or similar — to be decided in ADR-004). The flow:

1. The engine **enqueues** a job — bounded by the activity's DataInputs, not the full instance context — onto the queue, keyed by topic, and **parks** the task. The engine holds no live call; only a queued job and a parked track, both persistable.
2. A worker **fetches-and-locks** jobs for the topics it can execute (declaring its capabilities via the fetch), executes locally, and **reports** the outcome (complete / status / BPMN error / technical fail).
3. The report re-enters the owning Orchestrator, which resumes the parked instance. A technical fail **re-enqueues with backoff** (job retries); a lock that expires without a report (worker crash) makes the job fetchable again.

**Why an engine-owned fetch-and-lock queue** *(revised — supersedes the earlier "direct dispatch, not a queue" sketch; full rationale in ADR-021 §2.4)*: pull decouples the engine from worker addressing and holds **no live in-flight call**, so an instance waiting on a worker is **dehydratable** (the job sits in the store, the track is parked) — directly enabling persistence-based failover (§13.3) instead of blocking it. Retry-as-re-enqueue and crash-resilience-as-lock-expiry fall out for free. The original concern — third-party broker dependency, extra failure domain, queue infrastructure most deployments don't need — is avoided by keeping the queue **engine-owned**, not an external broker: the default is an **in-memory** queue + local worker pool (zero extra infrastructure); a durable store arrives only with persistence (ADR-009), and a remote wire protocol only when a deployment needs out-of-process workers (ADR-004). The topology stays two-tier (engine + worker); failure handling still aligns with the Orchestrator that owns the instance.

This is **just another extension** implementing the `WorkerDispatcher` interface (§11) — no architectural change to core required. Library users who don't need it pay nothing for it (default impl is an in-process in-memory queue with a local worker pool).

### 13.3 Persistence and recovery as the foundation

All distribution modes — single-process restart, instance failover, multi-node deployment — rest on the engine's ability to **save and restore instance context** cleanly:

- Instance state checkpointed at every observable BPMN lifecycle transition (per [docs/bpmn-spec/state-machines/activity-lifecycle.md](../bpmn-spec/state-machines/activity-lifecycle.md)).
- On runtime start / restart, the runtime queries the `Repository` for in-flight instances and rehydrates them — re-spawning the event loop + track goroutines as needed.
- Long-wait states (UserTask, multi-day timers, awaiting external Message) do NOT hold goroutines — they release them and rely on rehydration when the trigger arrives. See §10.

Robust save/restore is a P0 quality (§6). Without it, neither restart recovery nor instance-level distribution is achievable.

### 13.4 Open question: cross-cluster shared state

When goBpm runs multi-node, certain BPMN constructs require cluster-wide visibility:

- **Signals** — a thrown Signal MUST reach all catching handlers across all instances, regardless of which node owns each instance.
- **Message correlation** — an arriving Message MUST find its target instance even if owned by a different node than the one receiving the Message.
- **Shared correlation keys** across long-lived Conversations spanning multiple instances.

These are solvable through the extension model:
- `MessageBroker` backed by Redis Streams / Kafka / etc. for inter-node message routing.
- Event broadcast layer (an extension of `EventHub`) for Signal distribution.
- DB-backed `Repository` providing cluster-shared visibility into in-flight instances.

The detailed design is **out of v.1 scope** — to be addressed in a future ADR (ADR-008) when concrete multi-node demand materializes.

### 13.5 Cluster-configuration validation (forward-looking note)

When goBpm runs in cluster mode, certain extension configurations are fundamentally incompatible — in-memory `Repository`, in-memory `MessageBroker`, in-memory `EventHub`, fake `Clock`, and so on cannot honor cluster semantics. Each adapter SHOULD declare its cluster compatibility via the `ClusterAware` optional interface (per [ADR-002 §8.3](ADR-002-extension-architecture.md)); the runtime layer validates declared compatibility at startup when `cluster_mode` is enabled, and refuses to start with incompatible adapters wired. The substantive treatment — routing strategies, signal-broadcast backplane requirements, the full hard-block / warn / forces-explicit-choice matrix — lives in the future ADR-008.

## 14. Conformance & Compliance Scope

`goBpm` targets **BPMN 2.0 Process Execution Conformance** (OMG spec §2.1.2) — the Common Executable Subclass (§2.1.3) plus the **ComplexGateway extension** above it.

Full normative reference lives at [docs/bpmn-spec/](../bpmn-spec/). The [conformance.md](../bpmn-spec/conformance.md) document is the authoritative in/out element list.

Conformance verification:
- Per-element implementation reviewed against [docs/bpmn-spec/elements/](../bpmn-spec/elements/) (structural attributes) and [docs/bpmn-spec/state-machines/](../bpmn-spec/state-machines/) + [docs/bpmn-spec/semantics/](../bpmn-spec/semantics/) (behavior).
- Conformance test suite (to be established): combination of MIWG public fixtures + project-internal element-coverage tests.
- Each released version pinned to a BPMN-spec snapshot SHA so conformance claims are reproducible.

### 14.1 Deliberate deviations from BPMN 2.0

Some normative behaviours of the standard are **intentionally not implemented** — not "not yet" (those are tracked in the roadmap as unbuilt elements), but a design decision *not* to implement them. They share one principle: **gobpm rejects hidden, data-driven control that the process diagram does not show.** Implicit behaviour a modeller cannot see on the diagram is unpredictable and unmodellable; where the standard expresses control through invisible data conditions, gobpm requires the modeller to express it **explicitly** with the constructs the diagram does show (events, gateways).

| BPMN behaviour (spec) | gobpm decision & why |
|---|---|
| **Data-availability wait** (§10.4.2) — an activity whose input data is unavailable *waits* until it becomes available. | **Not implemented.** A data wait is a hidden synchronization: a token sits and waits on a condition absent from the diagram. gobpm treats an unavailable *required* input as an **error/incident**, never a wait. A process that must pause until data is present models that with a catch event or a gateway — visible on the diagram. |
| **Multiple input/output sets + data-driven selection** (§10.4.2) — an activity may declare several `InputSet`/`OutputSet`s; the engine selects, in declaration order, the first whose data is available, with an IORule pairing inputs to outputs. | **Not implemented.** Selecting a set by which data happens to be available is hidden, non-diagram branching — the same hazard as the data wait — and the feature is near-unused in practice (tooling barely exposes it; engines barely implement it). gobpm models **one `InputSet` and one `OutputSet`** per activity; genuine alternative input/output modes are modelled with gateways or boundary events. The optional/required and while-executing distinctions are kept *within* the single set, so nothing practical is lost; the model is shaped so multi-set selection can be added as an extension if a real demand ever appears. |
| **Underspecified item-aware element** — BPMN makes an `ItemAwareElement`'s `itemSubjectRef`/structure optional (`0..1`), so a Property / DataObject MAY be declared with no structure and filled at runtime. | **Not supported.** In gobpm an `ItemDefinition`'s structure *is* its value — an immutable, typed `Variable[T]` bound at construction with no setter to install a value where none exists. A value-less item-aware element therefore can never be filled, so **a process declaring one cannot be executed**; it is rejected at snapshot/registration rather than admitted as a dead placeholder. The "declare empty, fill at runtime" intent is expressed with a **typed-zero value** (`NewVariable(0)` / `""`). |

These deviations are conformance-relevant (the two data-flow rows are part of §10.4.2) and are recorded here so a reader coming from another engine is not surprised; this section is the authoritative register of intentional gobpm non-implementations of the standard.

### 14.2 Deliberate extensions to BPMN 2.0

gobpm also adds capabilities **beyond** the standard, through the standard's own extensibility points. These are additive — they remove no conformant behaviour — and are recorded here so the divergence from a strict reading is explicit.

| gobpm capability | Standard basis & why |
|---|---|
| **Go operation with a data reader** — a `ServiceTask`'s `Operation` may be implemented as an in-process Go functor that receives a narrow, public, read-only data reader (process properties + the engine's runtime variables `STARTED_AT`/`STATE`/`TRACKS_CNT`, by name) and returns its result. It composes this with the standard message-in/message-out contract as the author chooses — reader only, message I/O only, or both (§8.4.3, §13.3.3). | The standard fixes only the Operation's *message contract*; `implementationRef` leaves the implementation **mechanism engine-defined**. A Go functor with a data accessor is one such mechanism. The split is by **execution locus**: an external (out-of-process) message operation stays pure and message-only by locus; ambient read access is confined to the in-process Go kind, so it does not bend the standard for conformant/external services. |

## 15. Repository & Release Strategy

### 15.1 Repository

**Multi-module monorepo.** Single git repo at `github.com/dr-dobermann/gobpm`. Multiple `go.mod` files at module roots inside the repo.

Justification:
- **Solo-developer cognitive load:** one repo, one issue tracker, one CI config, single PR per cross-cutting change.
- **Clean dependency isolation:** users importing `github.com/dr-dobermann/gobpm` (core) do not get `runtime/` or any `adapters/*` deps.
- **Independent versioning:** modules can release at their own pace (`core v0.5`, `runtime v0.2`, `adapters/postgres v0.1`).
- **Easy split-out later:** if monorepo becomes unwieldy, any submodule moves to its own repo in a single directory-move operation.

Justification details: **ADR-003 Module Layout**.

### 15.2 Release artifacts

| Audience | Artifact | Source |
|---|---|---|
| Library users | Go module `github.com/dr-dobermann/gobpm` | `go get` |
| Runtime operators (quickstart) | Single static binary `gobpm-server` with bundled in-memory defaults | `go install github.com/dr-dobermann/gobpm/runtime/cmd/gobpm-server` |
| Runtime operators (production) | Docker image with configurable adapters | Container registry (TBD: GHCR vs Docker Hub) |
| Modelers | (eventually) BPMN XML samples + working examples | `examples/` in repo |

Versioning follows semver per module. The core library is the version-of-record for "the BPMN engine"; runtime tracks its own version.

### 15.3 Release 0.1.0 — MVP element scope

`goBpm`'s conformance **target** is the full Common Executable Subclass + ComplexGateway (§14); 0.1.0 is the **first milestone toward** that target, not a reduction of it. The 0.1.0 element set is chosen by **real-world frequency**, not spec completeness: empirical BPMN-usage studies (zur Muehlen & Recker; large model-repository analyses) and BPMS-vendor telemetry consistently show a **Pareto distribution** — a core of ~10–15 element types covers ~80–90% of executable models, while most of the 100+ notation elements are rare. 0.1.0 delivers that high-frequency core so the engine is *usable for the majority of real automation* before the long tail is filled in.

**In 0.1.0 — all executable** (the planned element set is complete):

| Category | Elements |
|---|---|
| Events | None Start / None End; Intermediate Catch/Throw for **Timer**, **Message**, **Signal**; **Error End** (throw); **Terminate End** |
| Tasks | **Service**, **User**, **Send**, **Receive** |
| Gateways | **Exclusive**, **Parallel**, **Inclusive** (split + OR-join), **Complex**, **Event-Based** |
| Boundary events | interrupting + non-interrupting **Timer** / **Message** / **Signal** / **Error** boundary events |
| Messaging | cross-instance Message correlation (conversation keys) |

The three highest-frequency gaps that opened 0.1.0 have all landed: **boundary events**
([ADR-018 v.1](ADR-018-boundary-events-and-activity-interruption.md), SRD-029) — Timer-boundary first,
then Message/Signal/Error on the same infrastructure; **error handling** — Error End Event (throw) +
Error Boundary Event (catch) with `ErrorEventDefinition`/BpmnError propagation (epic #79; cross-scope
and Sub-Process propagation deferred with #85 to 0.2.0); and the **Terminate End Event**
(SRD-030) — abnormal whole-instance termination on the loop's native event lane, completing the
instance-termination story in the runtime ([ADR-001 v.6](ADR-001-execution-model.md) §4.6,
[ADR-006 v.2](ADR-006-events-and-subscriptions.md) §2.2).

**Deferred to 0.2.0:** Embedded **Sub-Process** and **Call Activity** (#85) — high value for reuse/structure, but a self-contained increment 0.1.0 does not block on.

**Deferred to later releases** (tracked as epics, ordered by frequency, not spec order): Script & Business-Rule/DMN tasks (#87), Multi-Instance / Loop (#88), Conditional events (#89), Compensation / Escalation / Cancel / Link events (#90), Transaction & Event Sub-Process (#91), Ad-hoc Sub-Process (#92), Data Objects / Data Store (#82), Timer persistence & hydration (#84), Observability / Event Core (#76), Fault Tolerance — incidents/retry/DLQ (#80), and the platform epics (versioning #94, migration #95, multi-tenancy/IAM #73, forms #75, expression layer #74, admin tools #96). **Manual Task** is deliberately deprioritised — the engine treats it as a pass-through (no token block), so it carries near-zero execution value.

**Permanent non-goals** are unchanged — see **§4** (no modeler, no DMN *engine*, no Choreography/Collaboration-metamodel execution, no DI, no BPEL, parser-as-separate-module) and the spec-level deviations in **§14.1**. The authoritative in/out element list remains [conformance.md](../bpmn-spec/conformance.md); this section is **release phasing** over it.

## 16. References

### Subordinate ADRs

| ID | Title | Status | Scope |
|---|---|---|---|
| ADR-001 | Execution Model | **Accepted v.3** | Two-layer Instance + track; one event-loop goroutine per instance; token as a projection; ctx cancellation cascade. (Joins/events/long-waits/persistence relocated to the ADRs below + the Persistence ADR.) |
| ADR-002 | Extension Architecture | **Accepted v.2** | Interface catalog; functional-options assembly; default implementations; adapter module conventions |
| ADR-003 | Module Layout | Draft | Multi-module monorepo; import directions; module evolution; future split-out path |
| ADR-004 | Runtime Environment Contract | Draft | Tenancy, AuthN, AuthZ, observability, diagnostics, profiling — ownership and interfaces |
| ADR-005 | Gateways & Joins | **Accepted v.2** | Synchronizing join, non-synchronizing merge, OR-join, Event-Based Gateway + `Withdrawn`; fork-flow activation by gateway type |
| ADR-006 | Events & Subscriptions | **Accepted v.1** | EventHub delivery, Terminate End Event, interrupting boundary events, wait nodes |
| ADR-007 | In-Memory Long Waits | Draft | Subscription → goroutine ends → re-spawn (durable version → Persistence ADR) |
| ADR-008 | Distribution & Scale | planned | The §13 preliminary content, when multi-node demand materializes |
| ADR-009 | Per-Instance Node Graph | **Accepted v.1** | Node-owned runtime state; each instance clones the node graph — resolves the ADR-001 §4.7 deferral and eliminates the shared-node data race |
| ADR-010 | Process Data Model | **Accepted v.2** | Container-scope data plane + per-execution frames; §2.7 addressable data access (default scope by name + named `SOURCE/address` providers) |
| ADR-011 | Process Data Flow | **Accepted v.6** | One input/output set per activity (per-parameter flags, no Set type); availability-gated start; polymorphic Operation (message + in-process Go kinds). v.6: **structural data** — the `Value` family gains a `Record` capability beside `Collection` (navigable `scalar｜list｜record`, schema-by-traversal); path addressing (`order.items[0].price`) in the data-access seam serving mappings/expressions/conditions; commit-diff change detection; native-struct interop via a per-type adapter registry (registration-time reflection standard, codegen upgrade). S1 (read path) + S2 (write path) + S3 (commit-diff + DataChange facts) landed (SRD-042/043/044); S4 adapters is the remaining slice |
| ADR-012 | Execution Layering | **Accepted v.1** | Execution contracts relocated to public `pkg/exec`/`renv`/`eventproc`/`interactor`; `pkg/model` imports no `internal/*` (`model-no-internal` depguard) |
| ADR-013 | Observability & Control | **Accepted v.2** | v.1: the InstanceHandle + one lifecycle channel nodes plug into. v.2: the observable-event taxonomy engine-wide (13 kinds, open phases, reserved slots), one producer feeding the observer stream AND the operator-log echo, the engine-scope observer registry, and the visibility-policy seam (optional capabilities on the auth extension, pass-through default) — DataChange emission landed via the ADR-011 commit-diff (SRD-044): all 13 kinds emit |
| ADR-014 | Message Handling | **Accepted v.1** | SendTask/ReceiveTask + throw/catch message events over a pluggable `MessageBroker` via the node-agnostic `MessageWaiter`; producer/consumer seam; `Envelope` |
| ADR-015 | Event-Triggered Instantiation | **Accepted v.1** | A message start event / instantiate ReceiveTask spawns an instance via a definition-level instance-starter; born-from-event seeding; manual-start opt-out |
| ADR-016 | Message Correlation | **Accepted v.1** | Message-to-instance resolution (route / create / hold); key-based correlation (composite key derived from the payload); conversation-token threading (phase-2c) implemented via SRD-015/SRD-017 — multi-key, lazy secondary-key init, mismatch guard; context-based correlation (phase-3) decided-but-deferred |
| ADR-017 | Channel-Based Event Processing | **Accepted v.1** | Per-track buffered channel park (deferred choice atomic by construction) + the loop as sole owner of delivery and token positions; removes the busy-spin and the cross-goroutine reads |
| ADR-018 | Boundary Events & Activity Interruption | **Accepted v.1** | Loop-owned boundary watches over the guarded activity's window; per-track cancel context; interrupting/non-interrupting discrimination on fire; Error boundaries matched at the fault point |
| ADR-019 | Definition Versioning | **Accepted v.1** | `RegisterProcess` returns a registration handle naming `(key, version)`; frozen per-version snapshots; start by handle / key+version / latest; latest-supersedes auto-start, promote-on-removal |
| ADR-020 | Human Interaction Execution Model | **Accepted v.1** | UserTask as a wait node on the same park seam as events; Camunda-style triad authorization over an `Actor`; `TaskDistributor` announce/withdraw boundary + `TaskView`; ManualTask pass-through |
| ADR-021 | Service Task Execution Model | **Accepted v.1** | Two execution loci: in-process (`WithTimeout`) and external workers over an asynchronous fetch-and-lock job queue; declarative outcome classification (`ErrorMapper`), retry policy, output mapping, and the worker-trust knob |
| ADR-022 | Error Propagation & Logging Policy | **Accepted v.1** | Handle every error exactly once (log XOR return); fail-fast vs best-effort judged by the failure surface; enumerated handling boundaries; level discipline; one canonical log-attribute vocabulary; silence is opt-out |

### Reference material

- [docs/bpmn-spec/](../bpmn-spec/) — BPMN 2.0 Process Execution Conformance KB
- [docs/bpmn-spec/conformance.md](../bpmn-spec/conformance.md) — in-scope / out-of-scope element list
- [docs/analytics/Analysis of the gobpm project.md](../analytics/Analysis%20of%20the%20gobpm%20project.md) — prior analysis
- [docs/analytics/gobpm Development Roadmap.md](../analytics/gobpm%20Development%20Roadmap.md) — phased roadmap
- BPMN 2.0 specification PDF: `docs/BPMN formal-13-12-09.pdf` (OMG formal/2013-12-09, v2.0.2)

## Appendix A — Glossary

| Term | Meaning |
|---|---|
| **Engine** | The top-level façade exposed by the core library. Holds extension implementations and the Process registry. |
| **Process** | A BPMN 2.0 Process definition (model). |
| **Snapshot** | An immutable, validated representation of a Process. The Engine accepts a Snapshot, not a mutable model. |
| **Process Instance** | A running execution of a Process. Owned by one Orchestrator goroutine. |
| **Orchestrator** | The goroutine owning a single Process Instance's state. Receives token events, applies state transitions. |
| **Token** | The BPMN-theoretical concept of "execution presence" at a flow node. In goBpm it is a *projection* of a track's current step (computed on demand), not a stored object; the **track** is the goroutine that executes the node's behavior and reports back to the instance (per ADR-001 v.3). |
| **Rehydration** | Reconstruction of an in-memory Process Instance from its persisted state, when a long-wait trigger fires. |
| **Extension interface** | A Go interface defining an extension point (Repository, Logger, Tracer, ...). Default impl ships in core; production impls in adapter modules. |
| **Adapter module** | A module under `adapters/*` providing a concrete implementation of one or more extension interfaces. |
| **Runtime** | The `runtime/` submodule. The standalone server hosting the engine, providing HTTP/gRPC API, multitenancy, AuthN/Z, observability. |

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-05-29 | Ruslan Gabitov | Initial draft, incorporating first review round: §1.1 document-class taxonomy; observability default = **visible** (`slog.Default()`) with explicit opt-out for low-noise environments; §9.2 scaffold modules upfront, not incrementally; §10 emphasize save/restore + recovery as P0; §11 add `WorkerDispatcher` extension; §13 new "Distribution & Scale" section flagged **preliminary, subject to refinement** (deferred for deeper discussion before SAD acceptance); N8 clustering reframed from non-goal to additive overlay; `thresher` name retained for the engine façade. |
