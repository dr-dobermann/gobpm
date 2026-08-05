---
title: gobpm Developer Manual
description: Build, run, control, and extend the gobpm BPMN 2.0 engine — the entity stack, the taxonomies, the runtime, and every extension seam, grounded in the public API.
---

# gobpm Developer Manual

**gobpm** is an embeddable BPMN 2.0 process-execution engine for Go. This manual
is the developer reference — the whole entity stack from `foundation.BaseElement`
to the `Thresher` engine, the element taxonomies, how the runtime executes and
processes events, how to control processes and instances, and how to extend the
engine with your own implementations. Every page is grounded in the public API
(`go doc`) and a runnable [`example`](../../examples/); the *why* behind the
design lives in [`docs/design/`](../design/index.md).

New here? Start with **[Your first process](getting-started/first-process.md)**.

## Part 1 — Getting started

- [Installation](getting-started/installation.md) — add gobpm to a Go project.
- [Your first process](getting-started/first-process.md) — start → task → end, running your own Go code. *(`basic-process`)*
- [Running & observing](getting-started/running-and-observing.md) — the engine lifecycle and watching an instance. *(`basic-process`, `data-change`)*

## Part 2 — Architecture & runtime

- [Architecture overview](concepts/architecture.md) — the layer stack (model → snapshot → instance → engine) and how they relate.
- [The entity stack](concepts/entities.md) — `BaseElement` → `Identifyer` → flow node → activity/event/gateway → `Process` → snapshot → instance/track/token → `Thresher`. *(`pkg/model/foundation`, `pkg/model/flow`)*
- [Process, instance, track, token](concepts/execution-model.md) — how a definition becomes running work.
- [How a process executes](concepts/process-execution.md) — the run loop, node execution, the data phases (`LoadData`/`Exec`/`UploadData`/commit). *(`pkg/exec`, `pkg/renv`)*
- [How events are processed](concepts/event-processing.md) — the EventHub, waiters, correlation, delivery. *(`message-send-receive`, `signal-broadcast`)*
- [Scope & the data plane](concepts/scope-and-data.md) — where data lives and name resolution by walk-up. *(`process-data`)*
- [The engine (Thresher)](concepts/engine.md) — construction, configuration options, lifecycle (`Run`/`Stop`). *(`pkg/thresher`)*
- [Observability](concepts/observability.md) — facts, reporters, observers, the operator log. *(`pkg/observability`, `data-change`)*

## Part 3 — The value & data model

- [The value model](data/value-model.md) — `Value` and the four kinds (`Collection`/`Record`/`Map`); the tiers. *(`pkg/model/data`, `pkg/model/data/values`)*
- [Item definitions & item-aware elements](data/item-definitions.md) — `ItemDefinition`, `ItemAwareElement`, `Property`, `DataState`. *(`pkg/model/data`)*
- [Reading & writing by path](data/structural.md) — records, lists, maps; assembling nested output. *(`structural-data`, `structural-output-mapping`, `maps`)*
- [Native Go structs](data/native-structs.md) — wrap your own types as live process data. *(`native-structs`)*
- [Expressions](data/expressions.md) — conditions and computed values. *(`expression-routing`)*
- [Data Objects](data/data-objects.md) — scope-resident named containers. *(`process-data`)*
- [Data Store](data/data-store.md) — engine-global cross-instance storage. *(`data-store`)*

## Part 4 — Element reference

- [Foundation elements](foundation/index.md) — `BaseElement`, `Documentation`, `Identifyer`, the shared attributes every element carries. *(`pkg/model/foundation`)*
- [Sequence flows](foundation/flows.md) — connecting nodes; `flow.Link`, conditions, defaults, and how a token traverses/splits at runtime. *(`pkg/model/flow`)*
- [Data associations](foundation/data-associations.md) — the data edge; `AssociateSource`/`AssociateTarget`, source/target routing, transformations. *(`pkg/model/data`)*

**Tasks** — [Activities taxonomy](tasks/index.md) *(`pkg/model/activities`)*
- [Service Task](tasks/service-task.md) *(`service-task-worker`)* · [User Task](tasks/user-task.md) *(`usertask`)* · [Script Task](tasks/script-task.md) *(`script-task`)* · [Business Rule Task](tasks/business-rule-task.md) *(`business-rule-task`)* · [Send / Receive Task](tasks/send-receive-task.md) *(`message-send-receive`)* · [Manual Task](tasks/manual-task.md)

**Gateways** — [Gateways taxonomy](gateways/index.md) *(`pkg/model/gateways`)*
- [Exclusive](gateways/exclusive.md) · [Parallel](gateways/parallel.md) · [Inclusive](gateways/inclusive.md) · [Complex](gateways/complex.md) · [Event-based](gateways/event-based.md)

**Events** — [Events taxonomy](events/index.md) *(`pkg/model/events`)*
- [Start & End](events/start-and-end.md) · [Timer](events/timer.md) · [Message](events/message.md) · [Signal](events/signal.md) · [Error](events/error.md) · [Escalation](events/escalation.md) · [Conditional](events/conditional.md) · [Link](events/link.md) · [Terminate](events/terminate.md) · [Compensation](events/compensation.md) · [Boundary events](events/boundary.md) · [Event sub-processes](events/event-subprocess.md)

**Sub-processes & reuse** — [Composition taxonomy](subprocesses/index.md)
- [Embedded Sub-Process](subprocesses/embedded.md) · [Call Activity](subprocesses/call-activity.md) · [Transaction Sub-Process](subprocesses/transaction.md)

**Iteration** — [Iteration taxonomy](iteration/index.md)
- [Standard Loop](iteration/standard-loop.md) · [Multi-Instance](iteration/multi-instance.md)

## Part 5 — Controlling processes & instances

- [Registering & versioning](operating/registering-and-versioning.md) — `RegisterProcess`, versions, latest vs pinned. *(`versioning`)*
- [Starting instances](operating/starting-instances.md) — `StartLatest`/`StartVersion`, the instance handle. *(`basic-process`)*
- [Instance lifecycle](operating/instance-lifecycle.md) — wait for completion, cancel, inspect state. *(`basic-process`, `terminate-end-event`)*
- [Observability in practice](operating/observability.md) — subscribe, filter facts, tune the log level. *(`data-change`)*
- [Correlation & conversations](operating/correlation.md) — route messages to the right instance. *(`inter-instance-correlation`, `conversation-routing`)*
- [External workers](operating/external-workers.md) — fetch-and-lock job execution. *(`service-task-worker`)*
- [Human tasks](operating/human-tasks.md) — the task distributor: list, assign, complete. *(`usertask`)*
- [Persistence & recovery](operating/persistence.md) — instance checkpoints, restart recovery, dehydration (a long wait costs no goroutines), and safely sharing one store between engines. *(`restart-recovery`)*

## Part 6 — Extending gobpm

Each page: the seam interface, the registration call, a minimal real implementation, and how the engine uses it.

- [Custom ID generator](extending/id-generator.md) — `foundation.IDGenerator` + `foundation.SetGenerator`.
- [Custom Value type](extending/value-type.md) — `adapters.Register[T](build func(*T) data.Value)`.
- [Custom Operation](extending/operation.md) — `service.Operation` (beyond `gooper`).
- [Custom expression engine](extending/expression-engine.md) — `expression.Engine` + `thresher.WithExpressionEngine`.
- [Custom rule engine](extending/rule-engine.md) — `rules.Engine` + `WithRuleEngine`. *(`adapters/dtable`)*
- [Custom script engine](extending/script-engine.md) — `script.Engine` + `WithScriptEngine`. *(`adapters/lua`)*
- [Custom Data Store](extending/data-store.md) — `datastore.DataStore` + `WithDataStore`.
- [Custom repository](extending/repository.md) — `repository.Repository` + `WithRepository`.
- [Dehydratable waits](extending/dehydratable-waits.md) — `renv.Dehydratable` + `exec.WaitHolders`.
- [Custom message broker](extending/message-broker.md) — `messaging.MessageBroker` + `WithMessageBroker`.
- [Custom clock](extending/clock.md) — `clock.Clock` + `WithClock`.
- [Custom observability](extending/observability.md) — `Observer`/`Reporter`/`Logger`/`Tracer`/`MetricsRecorder`.
- [Custom worker dispatcher](extending/worker-dispatcher.md) — `tasks.WorkerDispatcher` + `WithWorkerDispatcher`.
- [Custom task distributor](extending/task-distributor.md) — `interactor.TaskDistributor` + `WithTaskDistributor`.
- [Custom authorization](extending/authorization.md) — `auth.AuthorizationProvider` + `WithAuthorizationProvider`.
- [Interchange converters](extending/converters.md) — `convert.Importer`/`Exporter` + `RegisterImporter`/`RegisterExporter`; BPMN 2.0 XML in/out. *(`pkg/convert/bpmn`)*

## Part 7 — Reference

- [Engine options catalog](reference/engine-options.md) — every `thresher.With*` option. *(`pkg/thresher`)*
- [Package map](reference/package-map.md) — what lives where.
- [Glossary](glossary.md) — BPMN and gobpm terms.
- [Examples index](../../examples/README.md) — every runnable program.
