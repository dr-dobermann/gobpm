---
title: Glossary
description: BPMN and gobpm terms used across the guides.
---

# Glossary

A one-stop reference for the words these guides lean on. Most are standard
BPMN 2.0 vocabulary; a few name gobpm's own runtime machinery (the **Thresher**,
the **Fact/Reporter/Observer** trio, the **functor**). Where a term has a
dedicated page, follow the link there for the worked example.

## How the terms fit together

You build a **process** (a definition), register it with the **engine**, and
start an **instance**. Inside the instance, work advances as **tokens** carried
along **tracks**; each running node reads and writes the **data plane** by name.

```mermaid
flowchart LR
    proc["process (definition)"] -->|RegisterProcess| tmpl["launch template (snapshot)"]
    tmpl -->|StartLatest| inst["instance"]
    inst --> track["track (goroutine)"]
    track --> token["token"]
    inst --> data["data plane (scope)"]
    inst -->|Fact| obs["Reporter / Observer"]
```

## Core execution

- **Process** — the static definition you build once: flow nodes (events, tasks,
  gateways) wired by sequence flows. It never runs; it is registered as a launch
  template. Built with `process.New(...)`.
- **Instance** — one live execution of a process. Each start clones the template
  into an independent run with its own data and state. Type: `internal/instance`
  `Instance`.
- **Token** — the moving "here is where control is" marker. It enters at a start
  event and flows node → node along the sequence flows.
- **Track** — the goroutine that carries a token. A diverging parallel gateway
  spawns a track per branch (branches run concurrently); a converging gateway
  waits for every inbound track before one token leaves.
- **Engine / Thresher** — the process orchestrator (`pkg/thresher`), created
  with `thresher.New`. You register processes with it, `Run` it, then
  `StartLatest` an instance and wait on the returned handle.
- **Snapshot / launch template** — the immutable, validated form a process is
  frozen into at registration. Every instance `Clone`s it; the snapshot is *not*
  a durable persistence mechanism (see [design ADR-009](../design/)).
- **Handle** — what `StartLatest` returns: your grip on one running instance.
  Call `WaitCompletion(ctx)` to block until it finishes and read the terminal
  state.

See [Process, instance, track, token](concepts/execution-model.md).

## Flow structure

- **Flow node** — any node control passes through: an event, task, gateway, or
  sub-process.
- **Sequence flow** — the directed connector between two flow nodes; `flow.Link`
  wires them. It may carry a condition.
- **Gateway** — a routing/synchronization node. **Exclusive** routes one branch,
  **Parallel** forks/joins all, **Inclusive** every-true, **Complex** by an
  activation threshold, **Event-based** defers to the first event.
- **Event** — something that happens: a **start** instantiates, an **end**
  completes, an **intermediate** waits or throws, a **boundary** event arms on an
  activity to interrupt it.
- **Activity / Task** — a unit of work. A **service task** runs your Go code, a
  **user task** is a human step, a **script task** an inline expression.

## Tasks & your code

- **Functor** — the plain Go function your service task runs. It receives a
  read-only `DataReader` and returns an optional result. Wrapped as an operation
  with the `gooper` helper:

```go
op, _ := gooper.New("greet",
    func(ctx context.Context, r service.DataReader,
        _ *data.ItemDefinition) (*data.ItemDefinition, error) {
        user, _ := r.GetData("user_name")            // a process property
        started, _ := r.GetData("RUNTIME/STARTED_AT") // an engine runtime var
        fmt.Printf("  ▶ hello, %v\n", user.Value().Get(ctx))
        return nil, nil
    })
```

- **Operation** — the invocable a task carries; `gooper.New` builds one from a
  functor (`pkg/model/service`).
- **Worker (external)** — a fetch-and-lock job executor that runs a service task
  out-of-process instead of in-process.

## The data plane

- **Data plane / Scope** — where an instance's data lives and is resolved *by
  name* (`internal/scope` `Scope`). Properties, data objects, and runtime
  variables all sit here.
- **Property** — a named value declared on the process, readable/writable by
  tasks (`data.Property`, added with `data.WithProperties`).
- **Item definition** — the typed container behind a named value
  (`data.ItemDefinition`); it wraps the actual `Value`.
- **Data path** — the dotted name used to read/write nested structural data, e.g.
  `receipt.sum`. Runtime variables use a `SOURCE/addr` form, e.g.
  `RUNTIME/STARTED_AT`.
- **Data state** — the lifecycle marker on a data element (`Ready`,
  `Unavailable`, …). Register the standard set once with
  `data.CreateDefaultStates()` before building data-carrying elements.

See the [data overview](data/overview.md).

## Events & routing

- **Event hub** — the engine's central event distributor
  (`internal/eventproc/eventhub` `EventHub`): it routes signals, messages, and
  timers to the waiters that expect them.
- **Waiter** — a parked catch that resumes when a matching event arrives.
- **Correlation** — matching an inbound message to the right instance by a
  correlation key. See [Correlation & conversations](operating/correlation.md).
- **Signal / Message** — a **signal** broadcasts to all listeners; a **message**
  is point-to-point to one correlated instance.

## Observability

- **Fact** — the single canonical event type the engine emits: identity, kind,
  phase and timing only — never process payload values
  (`pkg/observability` `Fact`).
- **Reporter** — the one producer behind every Fact; it echoes to the operator
  log and fans out to observers (`observability.Reporter`).
- **Observer** — your subscriber: any type with `OnFact(observability.Fact)`,
  registered via `engine.Observe`, returning a `Subscription` you cancel.
- **Kind / Phase** — a Fact's classification: **Kind** names the object class
  (`KindInstanceState`, `KindDataChange`, …); **Phase** the transition within it
  (`Started`, `Completed`, `Value_Updated`, …). Both are open vocabularies.
- **Operator log** — the synchronous `slog` echo of Facts for a human tailing
  output, distinct from the async observer stream.

See [Observability](concepts/observability.md) and
[Observability in practice](operating/observability.md).

## Sub-processes & iteration

- **Embedded sub-process** — a nested scope running in the same instance.
- **Call activity** — invokes another process as a separate child instance.
- **Transaction sub-process** — an ACID-like scope that aborts via a Cancel
  event.
- **Multi-instance** — fans a node out over a collection, sequentially or in
  parallel; a **standard loop** repeats one node while a condition holds.

See the [sub-processes](subprocesses/embedded.md) and
[iteration](iteration/standard-loop.md) sections.

## See also

- Full example: [`examples/basic-process/`](../../examples/basic-process/) — the
  smallest process, exercising process, instance, token, functor, and the data
  plane at once.
- Start here: [Your first process](getting-started/first-process.md)
- Concepts: [Process, instance, track, token](concepts/execution-model.md) ·
  [Observability](concepts/observability.md)
