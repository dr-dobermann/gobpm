---
title: gobpm User Guides
description: Learn to build and run BPMN 2.0 processes with the gobpm engine — concepts, every element, and working code.
---

# gobpm User Guides

**gobpm** is an embeddable BPMN 2.0 process-execution engine for Go. These
guides show how to *use* it — build a process, run it, and reach every BPMN
element — each page backed by a runnable program under
[`examples/`](../../examples/). For the *why* behind the engine's design, see
[`docs/design/`](../design/) (SAD/ADR/SRD).

New here? Start with **[Your first process](getting-started/first-process.md)**.

## Getting started

- [Installation](getting-started/installation.md) — add gobpm to a Go project.
- [Your first process](getting-started/first-process.md) — start → task → end, running your own Go code. *(`basic-process`)*
- [Running & observing](getting-started/running-and-observing.md) — the engine lifecycle, starting instances, watching them. *(`basic-process`, `data-change`)*

## Core concepts

- [The engine (Thresher)](concepts/engine.md) — register, run, start, wait.
- [Process, instance, track, token](concepts/execution-model.md) — how a definition becomes running work.
- [Scope & the data plane](concepts/scope-and-data.md) — where data lives and how it's resolved by name.
- [Events & the hub](concepts/events-and-hub.md) — how the engine routes signals, messages, timers.
- [Observability](concepts/observability.md) — facts, reporters, observers, the operator log.

## Building processes — tasks

- [Service Task](tasks/service-task.md) — run your own Go code (in-process or via a worker). *(`service-task-worker`)*
- [User Task](tasks/user-task.md) — a human step: assign, list, complete. *(`usertask`)*
- [Script Task](tasks/script-task.md) — an inline expression/script step. *(`script-task`)*
- [Business Rule Task](tasks/business-rule-task.md) — evaluate a decision table. *(`business-rule-task`, `decision-table`)*

## Building processes — gateways

- [Exclusive (XOR)](gateways/exclusive.md) — first-true routing + default flow. *(`gateway-routing`)*
- [Parallel (AND)](gateways/parallel.md) — concurrent split + synchronizing join. *(`parallel-gateway`)*
- [Inclusive (OR)](gateways/inclusive.md) — every-true split + OR-join. *(`inclusive-join`)*
- [Complex](gateways/complex.md) — activation-threshold join. *(`complex-gateway`)*
- [Event-based](gateways/event-based.md) — deferred choice; first event wins. *(`event-based-gateway`, `event-based-parallel-start`)*

## Building processes — events

- [Start & End](events/start-and-end.md) — instantiation and completion.
- [Timer](events/timer.md) — wait for a duration/instant. *(`simple-timer`, `timer-event`)*
- [Message](events/message.md) — send/receive across instances. *(`message-send-receive`, `message-intermediate-events`)*
- [Signal](events/signal.md) — broadcast to all listeners; signal start. *(`signal-broadcast`, `signal-start`)*
- [Error](events/error.md) — throw and catch a BPMN error; error boundary. *(`boundary-events`)*
- [Escalation](events/escalation.md) — non-fatal escalation throw/catch. *(`escalation-events`)*
- [Conditional](events/conditional.md) — wait on process data (false→true edge). *(`conditional-events`)*
- [Link](events/link.md) — off-page connectors within a process. *(`link-events`)*
- [Terminate](events/terminate.md) — end the whole instance/scope at once. *(`terminate-end-event`)*
- [Boundary events](events/boundary.md) — arm an event on an activity; interrupt or not. *(`boundary-events`)*
- [Event sub-processes](events/event-subprocess.md) — in-scope handlers, interrupting or not. *(`event-subprocess`)*

## Building processes — sub-processes & reuse

- [Embedded Sub-Process](subprocesses/embedded.md) — a nested scope in the same instance. *(`embedded-subprocess`)*
- [Call Activity](subprocesses/call-activity.md) — invoke another process as a child instance. *(`call-activity`)*
- [Transaction Sub-Process](subprocesses/transaction.md) — ACID-like abort via Cancel. *(`transaction-sub-process`)*

## Building processes — iteration

- [Standard Loop](iteration/standard-loop.md) — sequential, condition-driven repetition. *(`standard-loop`)*
- [Multi-Instance](iteration/multi-instance.md) — collection fan-out, sequential or parallel. *(`multi-instance-sequential`, `multi-instance-parallel`, `multi-instance-behavior`)*

## Working with data

- [Overview](data/overview.md) — the value model, tiers, reading & writing by path. *(`process-data`)*
- [Structural data](data/structural.md) — records, lists, maps by path. *(`structural-data`, `structural-output-mapping`, `maps`)*
- [Native Go structs](data/native-structs.md) — wrap your own types as process data. *(`native-structs`)*
- [Expressions](data/expressions.md) — conditions and computed values. *(`expression-routing`)*
- [Data Objects](data/data-objects.md) — scope-resident named containers. *(`process-data`)*
- [Data Store](data/data-store.md) — engine-global, cross-instance storage. *(`data-store`)*

## Operating gobpm

- [Observability in practice](operating/observability.md) — subscribe, filter facts, tune log level. *(`data-change`)*
- [Definition versioning](operating/versioning.md) — register versions, latest vs pinned. *(`versioning`)*
- [Correlation & conversations](operating/correlation.md) — route messages to the right instance. *(`inter-instance-correlation`, `conversation-routing`)*
- [External workers](operating/external-workers.md) — fetch-and-lock job execution. *(`service-task-worker`)*

## Reference

- [Glossary](glossary.md) — BPMN and gobpm terms.
- [Examples index](../../examples/README.md) — every runnable program, grouped by concern.
