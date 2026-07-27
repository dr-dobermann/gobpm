---
title: Package map
description: What lives in each public package.
---

# Package map

gobpm is one Go module — `github.com/dr-dobermann/gobpm` — split into two public
roots and an `internal/` you never import. `pkg/model/…` is **what you build**
(the BPMN element types, their options, the data model); `pkg/…` is **what the
engine runs on** (the Thresher, its runtime contracts, and the pluggable
extension seams). Optional adapters — real interpreters and stores — ship as
their own modules under `adapters/`. This page is the map: package → what lives
there, so you know where a symbol comes from before you reach for `go doc`.

> Only `pkg/…` and `adapters/…` are your API. `internal/…` (the EventHub,
> instance, scope, runner) is observable behavior, never a package you call —
> the public contracts in `pkg/exec`, `pkg/renv`, and `pkg/eventproc` are the
> seams the runtime exposes.

## Where most work happens

The handful of packages a typical embedder touches:

| Package | What you do with it |
|---|---|
| `pkg/model/process` | build a `Process` — the definition you register. |
| `pkg/model/activities` | tasks: Service, User, Script, Business-Rule, Send/Receive, Manual. |
| `pkg/model/events` | start/end, timer, message, signal, error, boundary, … |
| `pkg/model/gateways` | exclusive, parallel, inclusive, complex, event-based. |
| `pkg/model/flow` | wire nodes with sequence flows; conditions and defaults. |
| `pkg/model/data` | `ItemDefinition`, `Property`, `Parameter`, paths, expressions. |
| `pkg/model/service` + `.../gooper` | the `Operation` a Service Task runs; `gooper.New` wraps a Go func. |
| `pkg/thresher` | the engine — register, start, observe, `Run`/`Stop`, `With*` options. |
| `pkg/errs` | the `ApplicationError` every gobpm error is. |

## The model — `pkg/model/…`

The BPMN element stack you assemble a process from. Everything here is a type
you construct; `pkg/model/foundation` is the root every element embeds.

| Package | Import suffix | What lives there |
|---|---|---|
| `pkg/model/foundation` | `foundation` | base element types & interfaces — `BaseElement`, `Identifyer`, `Documentation`; `GenerateID`, `SetGenerator`, the `IDGenerator` seam. |
| `pkg/model/flow` | `flow` | flow nodes & connections — `Node`, `ActivityNode`, `SequenceFlow`, `Link`, `WithCondition`, `NodeType`. |
| `pkg/model/process` | `process` | the `Process` definition and its `New(name, …)` constructor. |
| `pkg/model/activities` | `activities` | activity/task implementations; `ActivityOption`, `WithCompensation`, `WithMultyInstance`, loop characteristics. |
| `pkg/model/events` | `events` | event implementations and their triggers; `WithInterrupting`, `WithCorrelationKey`, placement validators. |
| `pkg/model/gateways` | `gateways` | gateway implementations (exclusive/parallel/inclusive/complex/event-based). |
| `pkg/model/data` | `data` | the data model — `ItemDefinition`, `ItemAwareElement`, `Property`, `Parameter`, `DataState`, associations, paths, `FormalExpression`. |
| `pkg/model/data/values` | `values` | the concrete `Value` kinds (`Collection`/`Record`/`Map`) and path helpers (`SetPath`, `Walk`, `DiffValues`). |
| `pkg/model/data/adapters` | `adapters` | `Wrap(&yourStruct)` — a live `data.Record` view over a host Go struct (ADR-011 §2.9.5). |
| `pkg/model/data/goexpr` | `goexpr` | the Go-native `FormalExpression` implementation (a Go func as the evaluation core). |
| `pkg/model/data_objects` | `dataobjects` | BPMN `DataObject` — a per-instance, scope-resident named container. |
| `pkg/model/data_stores` | `datastores` | BPMN `DataStoreReference` — the flow-scope handle to the engine-global Data Store. |
| `pkg/model/expression` | `expression` | the `ExpressionEngine` seam (evaluation strategy is swappable); `goexpr` sibling is the default. |
| `pkg/model/service` | `service` | the `Operation` contract a Service Task runs and its `DataReader` read surface. |
| `pkg/model/service/gooper` | `gooper` | `gooper.New(name, fn)` — wrap a plain Go func as an `Operation`. |
| `pkg/model/hinteraction` | `hinteraction` | human-interaction model — `Actor`, `Assignment`, assignment slots for User Tasks. |
| `pkg/model/msgflow` | `msgflow` | message-flow choreography bridging a node's `Message` to the broker (ADR-014). |
| `pkg/model/artifacts` | `artifacts` | BPMN artifacts — `Artifact`, `Association` (annotations, groups). |
| `pkg/model/bpmncommon` | `bpmncommon` | shared model elements — `Message`, `CorrelationKey`, and other cross-cutting types. |
| `pkg/model/options` | `options` | the `options.Option` marker every constructor's `WithXxx` returns; `WithName`. |

## The engine & its runtime contracts — `pkg/…`

The Thresher plus the public interfaces the runtime exposes. `pkg/exec`,
`pkg/renv`, and `pkg/eventproc` are the seams that keep `pkg/model` free of any
`internal/` import — model elements implement/consume these, the engine supplies
the implementations.

| Package | Import suffix | What lives there |
|---|---|---|
| `pkg/thresher` | `thresher` | the engine — `InstanceHandle`, `InstanceState`, `Option` (`With*`), `Run`/`Stop`, registration & starting. |
| `pkg/exec` | `exec` | node-execution contracts (ADR-012) — `NodeExecutor`, `ActivationJoin`, data-binding consumer/producer, the `Frame` surface. |
| `pkg/renv` | `renv` | runtime-environment contracts — `EngineRuntime` (wired services) and per-execution `RuntimeEnvironment`. |
| `pkg/eventproc` | `eventproc` | event-production contracts — `EventProcessor` (a node handling a fired event), `EventProducer`. |
| `pkg/errs` | `errs` | the structured `ApplicationError` (message, `Classes`, `Details`) — every gobpm error. |
| `pkg/set` | `set` | a generic `Set[T comparable]` utility used across the model. |
| `pkg/tasks` | `tasks` | the external-worker contract — `WorkerDispatcher`, `RetryPolicy`, `ErrorMapper`, `OutputRule`, `WorkerOutcome`, `BpmnError`. |
| `pkg/interactor` | `interactor` | the human-task boundary — `TaskDistributor`, `TaskInfo`/`TaskView`, `TaskCompletion`, `HumanTask`. |

## The extension seams — `pkg/…` + their default sibling

Each engine service is an interface in a `pkg/…` root with a batteries-included
default in a **sibling subpackage**, swappable via a `thresher.With*` option.
See [Part 6 — Extending gobpm](../extending/id-generator.md).

| Seam package | Interface | In-core default | Referenced adapter |
|---|---|---|---|
| `pkg/clock` | `Clock` | `syscl` (system wall clock); `clocktest` fake for tests | — |
| `pkg/auth` | `AuthorizationProvider` | `allowall` (delegates to host) | — |
| `pkg/repository` | `Repository` | `memrepo` (in-memory) | `adapters/sqlite` (scaffold) |
| `pkg/datastore` | `DataStore`, `Registry` | `memstore` (in-memory) | — |
| `pkg/messaging` | `MessageBroker`, `Subscription`, `Envelope` | `membroker` (in-memory) | — |
| `pkg/observability` | `Logger`, `Reporter`, `Observer`, `Tracer`, `MetricsRecorder` | `noop`, `memmetrics`, `memtrace` (in-package) | `adapters/otel` (planned; core never imports OpenTelemetry) |
| `pkg/rules` | `rules.Engine` | `gorules` (Go decision registry) | `adapters/dtable` (DMN-shaped decision table) |
| `pkg/script` | `script.Engine`, `Registry` | empty `Registry` — `##None` (fails until you register) | `adapters/lua` (Lua via gopher-lua) |
| `pkg/model/expression` | `ExpressionEngine` | `goexpr` (Go-native) | — |
| `pkg/tasks` | `WorkerDispatcher` | `localdispatcher` (in-process) | remote HTTP/gRPC — future (ADR-004) |
| `pkg/model/foundation` | `IDGenerator` | built-in (`GenerateID`); replace via `SetGenerator` | — |

> `pkg/observability`'s `Logger` is satisfied directly by `*slog.Logger` — the
> default engine logs to `slog.Default()`, so observability is on unless you opt
> out. Core never imports OpenTelemetry; the OTel-shaped `Tracer`/`MetricsRecorder`
> bind through the planned `adapters/otel` module (ADR-002 §4.2).

## Optional adapter modules — `adapters/…`

Separate Go modules (own `go.mod`, `replace ../..` back to core) so the core
stays stdlib-light. Add one only when you need it.

| Module | Fills seam | What it is |
|---|---|---|
| `adapters/lua` | `script.Engine` | Lua via pure-Go gopher-lua — no cgo; a fresh sandboxed `LState` per run (ADR-031). |
| `adapters/dtable` | `rules.Engine` | Decision Table engine — DMN-shaped hit policy over an ordered rule list (ADR-029). |
| `adapters/sqlite` | `Repository` | SQLite-backed instance store — scaffold only at present (ADR-002/003). |

## See also

- [Engine options catalog](engine-options.md) — every `thresher.With*` in one place.
- [Glossary](../glossary.md) — BPMN and gobpm terms.
- [The entity stack](../concepts/entities.md) — how `foundation` → `flow` → activity/event/gateway → `Process` → engine layer.
- [Examples index](../../../examples/README.md) — every runnable program.
- Full API: `go doc github.com/dr-dobermann/gobpm/...`
