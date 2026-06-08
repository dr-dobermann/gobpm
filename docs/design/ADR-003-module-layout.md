# ADR-003 — Module Layout

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-05-30 |
| Owner | Ruslan Gabitov |
| Supersedes | — |
| Refines | [SAD-001 v.1 §9 Module Layout](SAD-001-vision-and-architecture.md) |

## 1. Context

SAD-001 §9 established the multi-module monorepo direction at the conceptual level (core library + future `runtime/` + future `adapters/*`, each its own Go module, all in one git repository). ADR-002 §4.2 listed the eleven extension interfaces in scope but explicitly deferred "exact subpackage layout" to this ADR. This ADR closes those questions:

- Which subdirectories are Go modules (have their own `go.mod`) vs ordinary subpackages?
- Where exactly do the ADR-002 extension interfaces live within `pkg/`?
- What are the import-direction rules between core, runtime, adapters, and the existing `internal/` glue?
- What's the tag convention for per-module semver?
- What's the migration path from the current single-module state?

Current code state (relevant to module layout):

- **One module today**: `github.com/dr-dobermann/gobpm` at the repo root (`go.mod`).
- **`pkg/`** contains: `errs/`, `model/`, `set/`, `thresher/`. Only `pkg/model/data/` exposes any extension-relevant types today (`FormalExpression`, `Source`, `PropertyAdder`).
- **`internal/`** contains: `eventproc/` (EventHub interface lives here), `exec/`, `instance/` (Instance + track; token is a projection — per ADR-001 v.3), `interactor/` (TaskDistributor-equivalent), `renv/` (RuntimeEnvironment lives here), `runner/`, `scope/`.
- **`examples/`** already follows the multi-module pattern — three subdirectories, each with its own `go.mod`.
- **No `runtime/`, no `adapters/`** yet.

Per ADR-002 §5, the engine-level extension accessors are factored into a public **`EngineRuntime`** interface promoted to `pkg/`. Three things an earlier draft planned to promote **stay internal** instead: `EventHub` (execution plumbing, not an extension point — ADR-002 §4.2); `RuntimeEnvironment` (it embeds internal types and now embeds the public `EngineRuntime`); and the human-interaction cluster `Registrator`/`Interactor` (its promotion + the `TaskDistributor` rename are deferred to a dedicated human-interaction ADR — ADR-001 v.4 §9). Eight new extension interfaces are introduced (Repository, Logger, Tracer, MetricsRecorder, Clock, MessageBroker, AuthorizationProvider, WorkerDispatcher) plus `ExpressionEngine` (which wraps existing `FormalExpression`). This ADR places them.

## 2. Decision

**Multi-module monorepo with one Go module per concern that has independent dependency footprint and release cadence. Inside the core module, `pkg/` exposes one subpackage per cohesive extension concern; `internal/` holds the implementation glue. Modules are added incrementally (`runtime/` first, then specific adapters) but their target directory positions and import-direction rules are locked now so adding a module is a directory creation, never a reorganization.**

Summary table:

| Concern | Decision |
|---|---|
| Core library module | `github.com/dr-dobermann/gobpm` at repo root — current state preserved |
| Public extension catalogue | One `pkg/` subpackage per cohesive concern (11 subpackages — see §4.2) |
| Default implementations | Bundled in their interface's package when small (no-op, simple in-memory); separated into a sibling subpackage when complex (e.g., `pkg/repository/memrepo/`) |
| Implementation glue | Stays in `internal/`; existing `eventproc/`, `interactor/`, `scope/`, `instance/`, `renv/`, `exec/`, `runner/` retain their roles |
| Runtime layer | `runtime/` submodule with its own `go.mod`. Scaffolded NOW (empty placeholder) per SAD-001 §9.2 to lock the boundary |
| Adapter modules | `adapters/<name>/` per adapter, each its own `go.mod`. `adapters/` directory created with at least one placeholder per ADR-002 §4.6 |
| Import direction | `core` → stdlib + `google/uuid` only; `runtime` → `core` + chosen adapters; `adapters/*` → `core` only; no cross-adapter imports |
| `pkg/` vs `internal/` rule | `pkg/` public APIs MUST NOT expose `internal/` types. `pkg/` MAY import `internal/` for implementation. `internal/` MAY import `pkg/` (e.g., `internal/instance/Instance` implements `pkg/renv.RuntimeEnvironment`) |
| Module versioning | Per-module semver. Core: `vX.Y.Z`. Submodules: `<path>/vX.Y.Z` per Go convention (e.g., `runtime/v0.2.0`, `adapters/postgres/v0.1.0`) |
| CI enforcement | golangci-lint depguard (or equivalent) enforces import-direction rules from day 1 — adding to existing `check.yml` per ADR-002 §8 recommendations |

## 3. Alternatives Considered

### 3.1 Module granularity

| Option | Description | Verdict |
|---|---|---|
| **Single module for everything** | One `go.mod` at root; all code (including future runtime + adapters) lives under it | Rejected. Dependency pollution — library users importing core would transitively pull runtime/adapter deps. Defeats SAD-001 G2 (minimal core deps). |
| **Multi-repo split (core + runtime + each adapter)** | Each is a separate git repo | Rejected per SAD-001 §14. Solo-dev cognitive overhead; coordinated changes become multi-repo PRs. Re-evaluated at scale only. |
| **Multi-module monorepo** — chosen | One git repo, multiple `go.mod` files in subdirectories | Selected per SAD-001. Solo-dev friendly; per-module dep isolation; per-module versioning; easy split-out later. |

### 3.2 `pkg/` subpackage granularity

Eleven extension interfaces from ADR-002 need places. Three packaging granularities considered:

| Option | Description | Verdict |
|---|---|---|
| **Single `pkg/extension/` for all interfaces** | One subpackage; users import one place | Rejected. Becomes a god-package over time; mixed responsibilities; harder to evolve individual concerns; doesn't match Go-stdlib convention (e.g., `io.Reader` lives in `io`, not in a unified `interfaces` package). |
| **One subpackage per individual interface** | `pkg/repository/`, `pkg/logger/`, `pkg/tracer/`, `pkg/metricsrecorder/`, `pkg/clock/`, etc. (~13 small packages) | Rejected. Too granular — Logger, Tracer, and MetricsRecorder share clear cohesion (all observability sinks); separating them into different packages forces awkward cross-package imports for users wiring observability. |
| **One subpackage per cohesive concern** — chosen | Group cohesive interfaces; separate dissimilar ones | Selected. See §4.2 for the concrete catalogue. |

### 3.3 Default implementation locality

| Option | Description | Verdict |
|---|---|---|
| **Defaults always in a sibling subpackage** — chosen | Every interface package contains only the interface (plus tightly-coupled types: option types, error sentinels, value types the interface returns). The default implementation lives in a sibling subpackage. Examples: `pkg/repository/` (interface only) + `pkg/repository/memrepo/` (in-memory default); `pkg/clock/` (interface only) + `pkg/clock/syscl/` (system-clock default). | Selected. Rationale: bundling the default in the interface package forces every importer to compile the default's code — including users who swapped to a non-default implementation. With subpackaging, adapter authors importing only `pkg/repository/` get just the interface; users who want the default explicitly import `pkg/repository/memrepo/`. Clean separation of concerns AND honest "pay for what you use" semantics. |
| **Defaults bundled in interface package** | One package holds both the interface and the default impl | Rejected per the above. Bundles force imports of code users may not need; obscure the interface contract behind the default's implementation noise. |
| **Defaults in a sibling module** (`gobpm-defaults`) | Per ADR-002 §3.4 reconsideration | Rejected per ADR-002 — adds module overhead for no real win. |

### 3.4 Existing `internal/` package retention vs reorganization

| Option | Description | Verdict |
|---|---|---|
| **Retain existing `internal/` structure** — chosen | `internal/instance/`, `internal/scope/`, `internal/eventproc/`, etc. keep their current roles; only the promoted INTERFACES move out to `pkg/` | Selected. The existing structure is well-thought-out (per ADR-001 + ADR-002 acknowledgments). Reorganizing for the sake of consistency would create noise without value. |
| **Reorganize `internal/` to mirror `pkg/`** | Internal packages renamed to match public ones | Rejected. The internal package names reflect implementation concerns (event-procession machinery, runner-orchestration, scope-tree) that don't perfectly map to user-facing extension concerns. Forced mirroring is over-engineering. |

### 3.5 Module versioning + tag convention

| Option | Description | Verdict |
|---|---|---|
| **Per-module semver with path-prefixed tags** — chosen | Core: `vX.Y.Z`. Submodules: `<path>/vX.Y.Z` (e.g., `runtime/v0.2.0`, `adapters/postgres/v0.1.0`) | Selected. Standard Go multi-module convention; tooling (Go module resolver) handles it natively. |
| **Synchronized versioning across all modules** | All modules share a single version number; bump together | Rejected. Forces unrelated releases; loses independent-evolution benefit of multi-module. |
| **No tags, branch-only versioning** | Use git branches as version markers | Rejected. Defeats `go get @version` semantic; `go.mod` works best with tags. |

### 3.6 Scaffolding cadence

| Option | Description | Verdict |
|---|---|---|
| **Scaffold all target modules upfront with placeholders** — chosen | Create empty `runtime/` and `adapters/` modules now, even if they have no real code | Selected per SAD-001 §9.2. Establishes import-direction discipline on day 1; first real code lands without restructuring; CI can enforce the boundary from the start. |
| **Add modules only when first real code is ready** | Wait until `runtime/` has a real server before creating its `go.mod` | Rejected. Risks the boundary not being respected once real code accumulates. The cost of a placeholder `go.mod` + `doc.go` is essentially zero. |

## 4. Decision Detail

### 4.1 Module layout (full target)

```
github.com/dr-dobermann/gobpm/                       ← repo root
├── go.mod                                            ← core module
├── cmd/                                              ← thin CLI entry points (current state)
├── pkg/                                              ← PUBLIC API
│   ├── thresher/                                     Engine façade + Option type
│   ├── model/                                        BPMN STANDARD types — everything from the spec lives under this tree
│   │   ├── activities/, events/, gateways/, flow/,   (existing BPMN element types)
│   │   │   data/, foundation/, …                     FormalExpression, Source, PropertyAdder stay in pkg/model/data/
│   │   └── expression/                               NEW — ExpressionEngine extension point (evaluates BPMN FormalExpression)
│   │       └── goexpr/                               Go-native default impl
│   ├── errs/                                         Errors (existing)
│   ├── set/                                          Utilities (existing)
│   ├── renv/                                         RuntimeEnvironment interface (interface-only; impl is internal/instance/Instance)
│   ├── repository/                                   Repository interface (interface-only)
│   │   └── memrepo/                                  In-memory default impl
│   ├── observability/                                Logger, Tracer, MetricsRecorder interfaces (interface-only)
│   │   ├── slog/                                     slog.Default()-based Logger default
│   │   └── noop/                                     No-op Tracer + MetricsRecorder defaults
│   ├── clock/                                        Clock interface (interface-only)
│   │   └── syscl/                                    System-clock default
│   ├── messaging/                                    MessageBroker interface (interface-only; EventHub stays internal)
│   │   └── membroker/                                In-memory MessageBroker default
│   ├── auth/                                         AuthorizationProvider interface (interface-only)
│   │   └── allowall/                                 Allow-all default
│   ├── tasks/                                        TaskDistributor + WorkerDispatcher interfaces (interface-only)
│   │   ├── localdistributor/                         In-process TaskDistributor default
│   │   └── localdispatcher/                          In-process WorkerDispatcher default
│   └── extension/                                    Optional capabilities (Starter, Stopper, HealthChecker)
├── internal/                                         ← PRIVATE IMPLEMENTATION (post-migration target)
│   ├── instance/                                     Instance, track, stepInfo; Token projection (per ADR-001 v.3)
│   ├── scope/                                        Scope tree implementation
│   ├── runner/                                       Process runner
│   └── exec/                                         Execution machinery
│   #
│   # NOTE: internal/eventproc/, internal/interactor/, internal/renv/ are
│   # removed after migration (see §4.6 step 12). Their interfaces moved to
│   # pkg/ and their default implementations moved to pkg/<concern>/<default>/
│   # subpackages. If any truly-internal helpers remain after the migration
│   # audit, they relocate to the nearest existing internal/ package or to a
│   # new internal/ package with a precise name reflecting just what stays
│   # internal.
├── examples/                                         ← multi-module, existing
│   ├── basic-process/                                Each its own go.mod
│   ├── simple-timer/
│   └── timer-event/
├── runtime/                                          ← SCAFFOLD NOW per SAD-001 §9.2
│   ├── go.mod                                        Submodule
│   ├── doc.go                                        Placeholder package doc
│   ├── cmd/gobpm-server/main.go                      Stub: prints "not yet implemented"
│   └── (server, tenancy, auth, obs, diag added later per ADR-004)
├── adapters/                                         ← SCAFFOLD NOW per SAD-001 §9.2
│   ├── memrepo-tests/                                Placeholder — conformance suite for Repository in-memory default
│   │   └── go.mod
│   └── (postgres, otel, etc. added as needed)
└── docs/                                             ← shared documentation
```

### 4.2 `pkg/` extension subpackage catalogue

Each subpackage holds the interface plus its default implementation(s). Subpackages chosen for cohesion — related interfaces grouped, unrelated ones separated.

All interface packages are **interface-only**. Defaults always live in sibling subpackages (per §3.3). Adapter authors and users who swap defaults pay nothing for the default impl's compiled code.

| Subpackage | Interfaces | Default impl location | Cohesion rationale |
|---|---|---|---|
| `pkg/thresher/` | `Thresher` (the engine façade); `Option` type; `WithRepository(...)`, `WithLogger(...)`, etc. | n/a — the engine itself is the entry point | The public engine entry point. |
| `pkg/renv/` | `RuntimeEnvironment` (extended per ADR-002 §4.3) | n/a — implemented by `internal/instance/Instance` | One interface, one package; central enough that nesting it elsewhere would obscure it. |
| `pkg/model/` | BPMN STANDARD types (existing — Activity, Event, Gateway, FormalExpression, …) | n/a — these are model types, not extension points | **Everything from the BPMN standard lives in this tree.** Extension points that evaluate BPMN concepts (e.g., `ExpressionEngine`) live as `pkg/model/<concern>/` subpackages, not at the `pkg/` root. |
| `pkg/model/expression/` | `ExpressionEngine` (NEW — extension point for evaluating BPMN `FormalExpression`) | `pkg/model/expression/goexpr/` (Go-native default) | Evaluates a BPMN spec concept (FormalExpression) — kept under the model tree per BPMN-standard-locality preference. |
| `pkg/repository/` | `Repository` | `pkg/repository/memrepo/` (in-memory, non-durable) | The persistence concern stands alone. |
| `pkg/observability/` | `Logger`, `Tracer`, `MetricsRecorder` | `pkg/observability/slog/` (slog-default Logger); `pkg/observability/noop/` (no-op Tracer + MetricsRecorder) | All three are observability sinks consumed together (a span typically logs + records metrics + adds attributes); separating their interfaces forces awkward multi-import wiring. Defaults split because slog is a real default; tracer/metrics defaults are no-ops. |
| `pkg/clock/` | `Clock` | `pkg/clock/syscl/` (system clock — `time.Now` wrapper) | Distinct from observability — used by Timer events, not by sinks; deserves its own package for testability injection. |
| `pkg/messaging/` | `MessageBroker` | `pkg/messaging/membroker/` (in-memory MessageBroker) | External message ingress / correlation. `EventHub` is **not** here — it stays internal (execution plumbing, ADR-002 §4.2). |
| `pkg/auth/` | `AuthorizationProvider` | `pkg/auth/allowall/` (allow-all default) | Standalone concern; identity-providers and tenancy belong in `runtime/`, not core. |
| `pkg/tasks/` | `WorkerDispatcher` | `pkg/tasks/localdispatcher/` (in-process default) | Remote-execution task dispatch. (`TaskDistributor` / human-interaction is **deferred** — ADR-001 v.4 §9 — so it is not in this package yet.) |
| `pkg/extension/` | `Starter`, `Stopper`, `HealthChecker` (optional side-capability interfaces per ADR-002 §8.3) | n/a — these are pure marker interfaces | The cross-cutting "lifecycle hook" trait set; lives alone so adapters can implement them without importing concern-specific packages. |

#### Conformance test helpers

Each interface package SHOULD ship its conformance helper as an exported function in a `<pkg>test` sibling subpackage (Go standard idiom, see `database/sql/driver/driverstest`, `net/http/httptest`):

| Test helper location | Helps test |
|---|---|
| `pkg/repository/repositorytest/` | Any `Repository` implementation |
| `pkg/messaging/messagingtest/` | Any `MessageBroker` implementation |
| `pkg/clock/clocktest/` | Any `Clock` implementation (incl. fake clocks for time-dependent tests) |
| `pkg/model/expression/expressiontest/` | Any `ExpressionEngine` implementation |
| `pkg/tasks/taskstest/` | Any `TaskDistributor` / `WorkerDispatcher` implementation |
| `pkg/auth/authtest/` | Any `AuthorizationProvider` implementation |

`Logger`, `Tracer`, `MetricsRecorder` don't need conformance suites — their interfaces are too simple (single-method sinks) to warrant one. Adapters self-test directly.

### 4.3 What moves to `pkg/` vs what stays in `internal/`

**Rule of thumb:** an interface goes public iff external adapters need to implement it. Once an interface is in `pkg/`, its default implementation belongs in the same `pkg/` subpackage (in-package or in a sibling subpkg if substantial) — NOT split across the public/private boundary. The `pkg/` subpackage is then self-contained from a user's import perspective.

What stays in `internal/` is purely-internal supporting machinery the public interface and its default impl rely on but which external adapters never need to touch.

#### What promotes to `pkg/` (interface + default impl together)

| Current location | Promoted to | What goes with it |
|---|---|---|
| `internal/eventproc/EventHub` (+ `eventhub/` impl) | **stays internal** | Execution plumbing, not an extension point (ADR-002 §4.2). No move. |
| `internal/renv` engine-level accessors → **`EngineRuntime`** | `pkg/renv/` (the public `EngineRuntime` interface only) | Only the engine-level accessors go public as `EngineRuntime` (implemented by `Thresher`). `RuntimeEnvironment` **stays in `internal/renv`** — it embeds internal `scope.Scope`/`EventProducer`/`RenderRegistrator` plus the public `EngineRuntime`; `Instance` implements it. |
| `internal/interactor/Registrator` cluster | **stays internal (deferred)** | Promotion + the `Registrator → TaskDistributor` rename are owned by a dedicated human-interaction ADR (ADR-001 v.4 §9). No move. |
| `pkg/model/data/FormalExpression` interface (already in `pkg/`) | `pkg/model/expression/ExpressionEngine` (new interface that wraps FormalExpression evaluation) | FormalExpression stays in `pkg/model/data/` — it IS a BPMN model element. ExpressionEngine is a new extension point that evaluates FormalExpressions; it lives under `pkg/model/expression/` because it directly evaluates a BPMN spec concept, and everything BPMN-adjacent stays in the `pkg/model/` tree. Default impl in `pkg/model/expression/goexpr/`. |

#### What stays in `internal/`

| `internal/` package | Role | Why internal |
|---|---|---|
| `internal/instance/` | `Instance`, `track`, `stepInfo` types + the `Token` projection per ADR-001 v.3 | The execution machinery is implementation; users interact with it via the `pkg/thresher/` façade and the `pkg/renv.RuntimeEnvironment` interface that `Instance` implements. (A future split of `track` into its own package behind a host interface is noted as deferred follow-up work.) |
| `internal/scope/` | Scope tree implementation backing `pkg/renv.RuntimeEnvironment.Scope()` | Implementation detail of how data scoping works; the `Scope` interface is exposed via the RuntimeEnvironment embedding. |
| `internal/runner/`, `internal/exec/` | Execution machinery (the orchestration loop, node-execution dispatch) | Implementation detail; no extension points here. |
| `internal/eventproc/` | The full event-distribution mechanism — `EventHub`, `EventProducer`, `EventProcessor`, `EventWaiter`, and the `eventhub/` impl | Execution plumbing; stays internal in full (ADR-002 §4.2). |
| `internal/interactor/` | The human-interaction cluster (`Registrator`/`Interactor`/`RenderController`) + impl | Stays internal; promotion deferred to the human-interaction ADR (ADR-001 v.4 §9). |
| `internal/renv/` | `RuntimeEnvironment` (embeds the public `EngineRuntime`) + composition glue | Stays internal; only `EngineRuntime` is promoted to `pkg/`. |

**The boundary discipline**: `pkg/` contains complete, self-contained extension contracts (interface + working default impl). External adapters and users import `pkg/` and get everything they need. `internal/` contains the engine machinery that consumes those `pkg/` interfaces but doesn't publish anything externally.

### 4.4 Import-direction rules

The rules are enforced by `golangci-lint depguard` (or equivalent) in CI from day 1.

| From → To | Allowed? | Notes |
|---|---|---|
| `cmd/*` → `pkg/*`, `internal/*` | YES | Top-level entry points compose everything. |
| `pkg/*` → `pkg/*` | YES | Public packages may import each other freely within the module. |
| `pkg/*` → `internal/*` | YES, with caveat | Permitted at the implementation level; the caveat: **`pkg/*` public APIs MUST NOT EXPOSE `internal/*` types in function signatures, struct fields, return types, or interface methods.** Implementation may use internal types; the contract surface MUST be public-types-only. |
| `internal/*` → `pkg/*` | YES | Common — e.g., `internal/instance/Instance` implements `pkg/renv.RuntimeEnvironment` so it imports the interface from `pkg/renv/`. |
| `internal/*` → `internal/*` | YES | Implementation packages cooperate freely. |
| `examples/*` → `pkg/*` | YES | Each example module imports core. |
| `examples/*` → `internal/*` | NO | Examples demonstrate the public surface; reaching into internal would mislead users. Enforced at the Go module level (Go's `internal/` rule already blocks external imports). |
| `examples/*` → `runtime/*`, `adapters/*` | NO | Examples demonstrate the embedded library use case; runtime/adapter wiring is its own example category later. |
| `runtime/*` → `pkg/*` | YES | Runtime composes the engine. |
| `runtime/*` → `internal/*` | NO | Runtime is a SEPARATE Go module; Go's `internal/` rule blocks the import at language level. This is the architectural enforcement. |
| `runtime/*` → `adapters/*` | YES, by user choice | Runtime imports the adapter modules the user wires in. |
| `adapters/*` → `pkg/*` | YES | Adapter implements public interfaces. |
| `adapters/*` → `internal/*` | NO | Adapter is a SEPARATE Go module; blocked by Go's `internal/` rule. |
| `adapters/*` → other `adapters/*` | NO | Hard rule (per ADR-002 §3.5 / §4.6). User composes shared resources at construction time. |
| `pkg/*` → `runtime/*` or `adapters/*` | NO | The core MUST NOT depend on its consumers. Strictly enforced. |

#### CI enforcement

Adding to `.github/workflows/check.yml`:

```yaml
- name: Enforce import-direction rules
  run: |
    # golangci-lint depguard configured in .golangci.yml
    golangci-lint run --disable-all --enable depguard ./...
```

`.golangci.yml` depguard config (illustrative; final form per implementation):

```yaml
linters-settings:
  depguard:
    rules:
      core-no-runtime:
        list-mode: lax
        files: ["pkg/**/*.go", "internal/**/*.go"]
        deny:
          - pkg: "github.com/dr-dobermann/gobpm/runtime"
            desc: "core MUST NOT import runtime"
          - pkg: "github.com/dr-dobermann/gobpm/adapters"
            desc: "core MUST NOT import adapters"
      examples-no-internal:
        files: ["examples/**/*.go"]
        deny:
          - pkg: "github.com/dr-dobermann/gobpm/internal"
            desc: "examples demonstrate public API only"
```

(Go's own `internal/` rule already blocks `runtime/` and `adapters/*` from importing core's `internal/` — the depguard rules above cover the cases Go's mechanism doesn't.)

### 4.5 Module versioning and release

Per-module semver. Tags use the standard Go multi-module convention:

| Module | Tag format | Go module path used by consumers |
|---|---|---|
| Core (`github.com/dr-dobermann/gobpm`) | `vX.Y.Z` | `go get github.com/dr-dobermann/gobpm@v0.5.0` |
| `runtime/` submodule | `runtime/vX.Y.Z` | `go get github.com/dr-dobermann/gobpm/runtime@v0.2.0` |
| `adapters/postgres/` | `adapters/postgres/vX.Y.Z` | `go get github.com/dr-dobermann/gobpm/adapters/postgres@v0.1.0` |
| `adapters/otel/` | `adapters/otel/vX.Y.Z` | (same pattern) |

Pre-1.0 (current): minor bumps may include breaking changes (Go semver convention). Post-1.0: discipline per ADR-002 §8.6 — add-only minor changes, deprecation paths, major version for breakage.

Pre-release tags: `vX.Y.Z-rc.N`, `vX.Y.Z-beta.N` per semver.

#### Adapter / runtime compatibility declaration

Each adapter / runtime module declares its minimum compatible core version in `go.mod`:

```
// runtime/go.mod
module github.com/dr-dobermann/gobpm/runtime
go 1.25
require (
    github.com/dr-dobermann/gobpm v0.5.0
)
```

Major version mismatch is a compile-time failure (Go's resolver enforces).

### 4.6 Migration from current state

Incremental, no big-bang reorg. Each step is a small focused change.

1. **Scaffold `runtime/` submodule.** Create `runtime/go.mod`, `runtime/doc.go`, `runtime/cmd/gobpm-server/main.go` (stub). Adds the boundary; no code yet.
2. **Scaffold `adapters/` directory.** Create at least one placeholder (e.g., `adapters/memrepo-tests/` with the conformance helper that the in-memory Repository default passes — establishes the adapter testing pattern).
3. **`EventHub` stays internal** — no move (execution plumbing, not an extension point; ADR-002 §4.2). `internal/eventproc/` keeps the full mechanism.
4. **Factor `EngineRuntime`** (the engine-level extension accessors) into `pkg/renv/` (public), implemented by `Thresher`. `RuntimeEnvironment` **stays in `internal/renv/`**, embedding the public `EngineRuntime`; `internal/instance/Instance` implements it.
5. **Human interaction is deferred** — the `internal/interactor/` cluster stays internal; the `Registrator → TaskDistributor` rename + promotion ride a dedicated human-interaction ADR (ADR-001 v.4 §9).
6. **Create new `pkg/` subpackages** for the seven net-new interfaces (Repository, Logger, Tracer, MetricsRecorder, Clock, MessageBroker, AuthorizationProvider, WorkerDispatcher, ExpressionEngine) with their default implementations.
7. **Add functional options** in `pkg/thresher/` (`WithRepository`, `WithLogger`, etc., per ADR-002 §4.4).
8. **Refactor `Thresher.New`** to accept options and wire defaults internally.
9. **Add `pkg/extension/`** with `Starter`, `Stopper`, `HealthChecker`.
10. **Add CI rule for import-direction enforcement** (golangci-lint depguard) to `.github/workflows/check.yml`.
11. **Add conformance test helper packages** (`pkg/repository/repositorytest/`, etc.) for the extension types where they apply.
12. **Remove only genuinely-empty `internal/` directories.** Note that `internal/eventproc/`, `internal/interactor/`, and `internal/renv/` **remain** (EventHub internal; human-interaction deferred; `RuntimeEnvironment` internal). Delete a directory only if it genuinely ends up empty; do NOT leave empty markers. Remove any other obsolete docs that surface during the migration audit.

Each step is its own SRD-class change (per project SDD discipline) after this ADR is Accepted.

## 5. Conception vs Current Code — Deliberate Departures

| Topic | Current state | This ADR | Required change |
|---|---|---|---|
| Number of modules | One (`go.mod` at root) | Three categories: core (1), runtime (1), adapters (N) — scaffolded incrementally | Add `runtime/go.mod`, `runtime/doc.go`, `runtime/cmd/gobpm-server/main.go` stub. Add `adapters/` directory with at least one placeholder per ADR-002 §4.6. |
| Extension interface location | `internal/eventproc/`, `internal/renv/`, `internal/interactor/`, `pkg/model/data/` (scattered; mostly internal) | The cohesive `pkg/*` subpackages of §4.2 (9 public extension contracts + `EngineRuntime`); `EventHub`/human-interaction/`RuntimeEnvironment` stay internal | Per the §4.6 migration list. |
| Default implementation location | Existing defaults are in `internal/*` packages (e.g., `internal/eventproc/eventhub/`) | **Always in a sibling subpackage** of the interface, never bundled in the interface package (§3.3). E.g., `pkg/repository/` has only the interface; `pkg/repository/memrepo/` has the in-memory default. Adapter authors and users who configure different impls pay nothing for unused defaults. | Move existing internal defaults to `pkg/<concern>/<default>/` subpackages. Empty resulting `internal/` directories are deleted (§4.6 step 12). |
| Thresher constructor | `Thresher.New(id string)` — no options | `Thresher.New(id, opts ...Option)` (per ADR-002 §4.4) | Implementation lives in `pkg/thresher/`; `Option` type defined there; per-extension `WithXxx` functions defined there. |
| Conformance test helpers | Not present | One `<pkg>test/` sibling subpackage per applicable interface | Add `pkg/repository/repositorytest/`, `pkg/messaging/messagingtest/`, etc. |
| Import-direction enforcement | None in CI | golangci-lint depguard rules in `.golangci.yml`, enforced by `check.yml` | Add depguard config and CI step. |
| `examples/*` content | Demonstrate `pkg/thresher/` + `pkg/model/` | Eventually demonstrate the extension wiring (each `WithXxx` shown in a small example) | New example files added as the extensions land; doesn't require restructuring. |
| Tag conventions | Single tag `v0.0.1` for the whole repo | Per-module tags using path-prefixed semver (`vX.Y.Z` for core; `<path>/vX.Y.Z` for submodules) | Update `make tag` target (per `chore/ci-audit` already merged on remote master) to default to core tags; document the submodule tag pattern in the Makefile. |

Each departure becomes a focused SRD-class implementation after this ADR is Accepted.

## 6. Consequences

### 6.1 Pros

- **Dependency isolation enforced architecturally.** Go's module system + `internal/` rule + CI-enforced depguard prevent the most common pollution paths.
- **Per-module evolution.** Core can ship v1.0 while runtime is still v0.x — independent stability contracts.
- **Solo-dev friendly.** One git repo, one issue tracker, one CI config, single PR per cross-cutting change. The multi-module overhead is a few `go.mod` files, not multi-repo coordination.
- **Adapter ecosystem enabled.** Clear pattern for what an adapter module looks like; clear contract for what it imports (core only); CI catches violations.
- **Migration is incremental.** Each step is small and well-bounded; no big-bang reorg.
- **Conformance testing standardized.** Every adapter passes the same suite as the in-memory default — same Go-test idiom (per ADR-002 §8.4).

### 6.2 Cons

- **More subpackages to maintain.** Eleven `pkg/*` subpackages vs the current four. Mitigated by each being small and focused.
- **`pkg/*` → `internal/*` rule is a convention, not a Go-language rule.** Linter enforcement catches most violations but it's not airtight. Discipline still required.
- **Adapter authors face per-module overhead.** Each adapter must maintain its own `go.mod`, version, and conformance tests. Mitigated by the small-and-focused-per-adapter pattern.
- **Multi-module tagging is more cognitive load.** `runtime/v0.2.0` is less familiar than `v0.2.0` to contributors coming from single-module projects. Documented in `CONTRIBUTING.md` and Makefile help.

### 6.3 Implications for adjacent decisions

- **ADR-001 Execution Model**: Instance's `internal/instance/` location preserved; its implementation of `pkg/renv.RuntimeEnvironment` is the bridge between internal types and public interface.
- **ADR-002 Extension Architecture**: this ADR places ADR-002's catalogue concretely. ADR-002's "exact subpackage layout deferred" is now resolved.
- **ADR-004 Runtime Environment Contract**: will use the `runtime/` submodule scaffolded here. Will define what goes inside `runtime/` (HTTP server, tenancy middleware, AuthN/Z integrations, observability stack, diagnostic endpoints, health-check endpoint).
- **SAD-001 §13 Distribution & Scale** (preliminary): `adapters/*` modules are where distribution adapters (gRPC worker dispatch, distributed message broker, etc.) live.

## 7. Verification

| What | How |
|---|---|
| **Module boundaries enforced** | CI step: `go mod tidy` + `go build ./...` succeeds across all modules. Manual: `go mod graph` from each module shows correct dependency tree (no cross-adapter, no core-to-runtime/adapter). |
| **Import-direction rules enforced** | CI step: `golangci-lint run --enable depguard` passes. Test: introduce a temporary forbidden import; assert CI fails. |
| **`pkg/*` doesn't expose `internal/*` types** | Static check via documentation tool (godoc) or manual review of `pkg/*` exported declarations. Audit can be scripted (parse exported types; ensure none come from `internal/`). |
| **Each extension interface has a default impl in core** | Per-interface test: `var _ Logger = (*defaultLogger)(nil)`; assert `pkg/observability` provides a default constructor that returns a working Logger. Repeat per interface. |
| **Conformance test helpers exist** | Per interface: `import _ "github.com/dr-dobermann/gobpm/pkg/repository/repositorytest"` succeeds and exposes the helper function. |
| **`runtime/` scaffold runs** | `go run github.com/dr-dobermann/gobpm/runtime/cmd/gobpm-server` prints the placeholder message; no panics. |
| **Per-module versioning works** | Test: tag `runtime/v0.0.1` locally; `go get github.com/dr-dobermann/gobpm/runtime@v0.0.1` resolves correctly (manual test using local replace directive). |
| **Examples still compile** | CI step: `cd examples/basic-process && go build ./...` (and similar for other examples). Each example imports only `pkg/`, never `internal/` or `runtime/`. |
| **Migration steps are independent** | Each of the 11 migration steps in §4.6 lands as its own commit; CI passes after each. No "big bang" required. |

**Acceptance gate** (Draft → Accepted): the layout is implemented per §4.1; CI enforces §4.4 import rules; the migration steps in §4.6 are executed (or planned + tracked as SRDs).

## 8. Enterprise-Readiness Recommendations

### 8.1 Package documentation

Every `pkg/` subpackage SHOULD have a thorough `doc.go` covering:
- **Purpose** — what the package solves in one sentence.
- **Stability contract** — what's stable, what's experimental.
- **Wiring example** — minimal code snippet showing how to use the interface with `Thresher`.
- **Default behavior** — what the package's defaults do (or don't do) so users know whether they need a real adapter.
- **See also** — pointer to relevant ADRs and bpmn-spec references.

`godoc` is the primary discovery surface for library users; investing in it pays back at every onboarding.

### 8.2 Adapter author template

A reference adapter SHOULD exist as a working template once `adapters/` has its first real adapter. The template includes:
- `go.mod` with correct core version constraint.
- `doc.go` per §8.1.
- Tests using the conformance helper from `<interface_pkg>test/`.
- A README documenting deployment requirements (e.g., "requires PostgreSQL 13+ with extension `pg_trgm`").
- A `CHANGELOG.md` per the project's bilingual / changelog conventions.

Without a template, the first three adapters land with three different file layouts. Establishing the template early keeps the ecosystem coherent.

### 8.3 Module version-bump discipline

When making a change in core that affects a published interface (e.g., adding a method to `Repository` because a new lifecycle event needs to be persisted), the version-bump cascade is:
- Core gets minor bump (or major if breaking).
- Each downstream adapter implementing the changed interface gets a corresponding minor bump.
- `runtime/` may need a bump if it wires the changed interface.

This cascade SHOULD be documented per-release in the changelog so adapter maintainers know what to retest.

### 8.4 Workspace mode for cross-module development

For local development across multiple modules (e.g., editing `core` and `adapters/postgres` simultaneously), use Go workspace mode (`go.work`). A `go.work` file at repo root:

```
go 1.25

use (
    .
    ./runtime
    ./adapters/postgres
    ./examples/basic-process
    ./examples/simple-timer
    ./examples/timer-event
)
```

`go.work` is `.gitignore`'d (it's developer-machine state). The pattern is documented in `CONTRIBUTING.md`. Without workspace mode, multi-module edits require `replace` directives in `go.mod` files which are easy to forget to revert.

### 8.5 Private vs published modules

If the project ever has internal adapters (e.g., a Darlean-specific adapter that isn't open-sourced), they live in a SEPARATE repository, not under `adapters/`. The `adapters/` directory is for adapters that ship with the project. Hosting closed-source modules under the open-source repo creates licensing and maintenance ambiguity.

The interface they implement is still `pkg/*`; the host project imports it via `go get` like any third-party module.

### 8.6 Long-term import stability

Once an interface is published in `pkg/*`, its import path is a stability contract. Renaming or moving a package after v1.0 is a major version bump per Go semver convention. The cost of moving `pkg/messaging/` → `pkg/msg/` post-1.0 is breaking every adapter and user.

**Implication**: choose package names carefully at v0.x. Once a name ships in a 1.0+ release, it's effectively permanent. Aliasing via `import obs "github.com/dr-dobermann/gobpm/pkg/observability"` lets users shorten import paths in their own code without forcing the canonical name to change.

## 9. References

- [SAD-001 Vision & Architecture](SAD-001-vision-and-architecture.md) — §9 Module Layout (this ADR refines); §14 Repository & Release Strategy
- [ADR-001 Execution Model](ADR-001-execution-model.md) — preserves `internal/instance/` structure that this ADR locks in
- [ADR-002 Extension Architecture](ADR-002-extension-architecture.md) — defines the 11 extension interfaces this ADR places; §3.5 cross-adapter rule formalized in §4.4 of this ADR
- Go modules reference: [Go Modules: Multi-module workspaces](https://go.dev/ref/mod#workspaces) — the `go.work` mechanism §8.4 references
- Go modules reference: [Tag versions for repositories with multiple modules](https://go.dev/ref/mod#vcs-version) — the path-prefixed tag convention §4.5 uses
- golangci-lint `depguard`: [docs](https://golangci-lint.run/usage/linters/#depguard) — the import-direction enforcement mechanism

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-05-30 | Ruslan Gabitov | Initial Draft. Pre-acceptance iteration ongoing; amendments folded into this Draft without per-round history rows (per project doc-history discipline). When v.1 flips to Accepted, this row records the Accepted state. |
