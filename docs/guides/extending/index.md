---
title: Extending gobpm
description: The engine's extension seams — swap in your own ID generator, value type, expression/rule/script engine, stores, brokers, clock, observability, and task handling.
---

# Extending gobpm

gobpm is built to be embedded and customized. Each seam is a small interface plus
a `thresher.With*` registration call, so you can replace a default with your own
implementation without touching the engine. Every page below covers the seam
interface, the registration call, a minimal real implementation, and how the
engine uses it.

- [Custom ID generator](id-generator.md) — `foundation.IDGenerator` + `foundation.SetGenerator`.
- [Custom Value type](value-type.md) — `adapters.Register[T](build func(*T) data.Value)`.
- [Custom Operation](operation.md) — `service.Operation` (beyond `gooper`).
- [Custom expression engine](expression-engine.md) — `expression.Engine` + `thresher.WithExpressionEngine`.
- [Custom rule engine](rule-engine.md) — `rules.Engine` + `WithRuleEngine`. *(`adapters/dtable`)*
- [Custom script engine](script-engine.md) — `script.Engine` + `WithScriptEngine`. *(`adapters/lua`)*
- [Custom Data Store](data-store.md) — `datastore.DataStore` + `WithDataStore`.
- [Custom repository](repository.md) — `repository.Repository` + `WithRepository`.
- [Custom message broker](message-broker.md) — `messaging.MessageBroker` + `WithMessageBroker`.
- [Custom clock](clock.md) — `clock.Clock` + `WithClock`.
- [Custom observability](observability.md) — `Observer`/`Reporter`/`Logger`/`Tracer`/`MetricsRecorder`.
- [Custom worker dispatcher](worker-dispatcher.md) — `tasks.WorkerDispatcher` + `WithWorkerDispatcher`.
- [Custom task distributor](task-distributor.md) — `interactor.TaskDistributor` + `WithTaskDistributor`.
- [Custom authorization](authorization.md) — `auth.AuthorizationProvider` + `WithAuthorizationProvider`.
