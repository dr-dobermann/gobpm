---
title: Engine options catalog
description: Every thresher option and what it configures.
---

# Engine options catalog

The engine is `thresher.New(id string, opts ...Option) (*Thresher, error)`. A
zero-option `New` already produces a fully working engine — every extension
defaults to its bundled core implementation. Each `With*` option overrides one
of those defaults; each `Without*` option strips a default behavior. This page
is the full catalog, grouped by what they configure, grounded in
`go doc github.com/dr-dobermann/gobpm/pkg/thresher`.

> `Option` is `func(*thresherConfig) error` — an option can reject bad input at
> `New`, which then returns the error (it never panics). Options are applied in
> order; a later duplicate of a *replace* option wins, while the two REPEATABLE
> registration options (expression / script engines) compose.

## Most uses

Most embedders never pass an option — the defaults run. The handful reached for
first:

| Option | What it configures |
|---|---|
| `WithLogger(l)` | the structured logger (default `slog.Default()`). |
| `WithScriptEngine(e)` | register a script engine — **required** before any Script Task runs (default: none). |
| `WithoutBanner()` | quiet the startup banner (handy in tests / servers). |
| `WithoutStartupConfig()` | quiet the startup configuration dump. |
| `WithDataStore(ref, store)` | register an engine-global Data Store for a `DataStoreReference`. |

## Engine extensions (`New` options)

Each replaces one bundled core implementation with your own. See
[Part 6 — Extending gobpm](../index.md) for the matching seam interface and a
minimal implementation.

| Option | Replaces | Default |
|---|---|---|
| `WithLogger(l observability.Logger)` | the structured logger | `slog.Default()` |
| `WithTracer(t observability.Tracer)` | the tracer | no-op |
| `WithMetricsRecorder(m observability.MetricsRecorder)` | the metrics recorder | in-memory registry |
| `WithClock(ck clock.Clock)` | the clock (all engine time reads through it) | system wall clock |
| `WithRepository(r repository.Repository)` | the process/instance repository | in-memory, non-durable |
| `WithMessageBroker(b messaging.MessageBroker)` | the message broker | in-memory inbox |
| `WithRuleEngine(e rules.Engine)` | the Business Rule Task's decision engine | in-core `gorules` registry |
| `WithAuthorizationProvider(a auth.AuthorizationProvider)` | the authorization provider | allow-all |
| `WithTaskDistributor(d interactor.TaskDistributor)` | the human-task distributor boundary | no-op (tasks still park, completable by id) |
| `WithWorkerDispatcher(d tasks.WorkerDispatcher)` | the external-worker dispatcher | in-process |

## Data Store registration

| Option | Effect |
|---|---|
| `WithDataStore(ref string, store datastore.DataStore)` | register the engine-global Data Store that a `DataStoreReference` with `dataStoreRef=ref` reads and writes (BPMN §10.4.1). Each store outlives every instance and is shared across them; call once per distinct store. Registering an already-used `ref` replaces it. |

See [Custom Data Store](../extending/data-store.md) and
[Data Store](../data/data-store.md).

## Expression & script engines (REPEATABLE)

These two register into a language/format-routed registry rather than replacing
a single slot — **each call adds another engine**, and duplicate language/format
claims fail `New` loudly.

| Option | Effect |
|---|---|
| `WithExpressionEngine(e expression.Engine)` | register an expression engine. Repeatable; language claims fold into the routing registry at `New`. The default batteries (e.g. `goexpr`) are prepended unless suppressed. |
| `WithoutDefaultExpressionEngines()` | start the expression registry EMPTY — no batteries prepended; every engine must register explicitly. An expression whose language nobody claims then fails loud, listing the registered claims. |
| `WithScriptEngine(e script.Engine)` | register a script engine for the Script Task. Repeatable; format claims fold into the routing registry at `New`. **Default: none** — with no engine registered, a Script Task fails with a wire-an-adapter error. |

See [Custom expression engine](../extending/expression-engine.md) and
[Custom script engine](../extending/script-engine.md).

## External-worker defaults (two-level config)

A worker-dispatched Service Task can carry its own per-task
`activities.WithRetryPolicy` / `WithErrorMapper` / `WithWorkerTrust`. These
engine-wide options set the **default** applied when a task carries no per-task
override — the second level of a two-level config.

| Option | Engine-wide default for |
|---|---|
| `WithWorkerRetryPolicy(p tasks.RetryPolicy)` | a worker task's technical-fault retry policy, absent a per-task `activities.WithRetryPolicy`. |
| `WithWorkerErrorMapper(m tasks.ErrorMapper)` | classifying a worker task's raw fault, absent a per-task `activities.WithErrorMapper`. |
| `WithWorkerTrustDefault(mode tasks.TrustMode)` | a worker task's trust mode, absent a per-task `activities.WithWorkerTrust`. An invalid mode is rejected. |

See [External workers](../operating/external-workers.md) and
[Service Task](../tasks/service-task.md).

## Startup output

The engine prints a banner and a configuration dump at `Run`. These strip them —
independently.

| Option | Effect |
|---|---|
| `WithoutBanner()` | suppress the startup banner block — ASCII wordmark, tagline, version / last-commit lines. The configuration dump still prints unless `WithoutStartupConfig` is also given. |
| `WithoutStartupConfig()` | suppress the startup configuration dump — the thresher id, the `configuration:` header, the per-extension lines. The banner still prints unless `WithoutBanner` is also given. |

Pass both for fully silent startup:

```go
eng, err := thresher.New("engine-1",
    thresher.WithoutBanner(),
    thresher.WithoutStartupConfig(),
)
```

## Registration options (not `New`)

One option is a `RegisterOption` — it configures a **process registration**, not
the engine. It is passed where a process is registered, not to `New`.

| Option | Effect |
|---|---|
| `WithManualStart()` | register a process as manual-start: the engine installs no persistent instance-starter, so no message spawns an instance — it starts only via `StartProcess`. Inside such an instance, message-start nodes seed as ordinary in-instance catches. An engine affordance (the default stays BPMN-conformant auto-instantiation) for tests and back-pressure control. |

See [Registering & versioning](../operating/registering-and-versioning.md).

## See also

- Concept: [The engine (Thresher)](../concepts/engine.md)
- Extending: [Part 6 — Extending gobpm](../index.md)
- Design: [ADR-002 — extension architecture](../../design/ADR-002-extension-architecture.md) · [ADR-030 — data objects & store](../../design/ADR-030-data-objects-and-store.md) · [ADR-031 — script task & script engine seam](../../design/ADR-031-script-task-and-script-engine-seam.md) · [ADR-032 — language-routed expression engines](../../design/ADR-032-language-routed-expression-engines.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/thresher`
