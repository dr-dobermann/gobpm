---
title: The engine (Thresher)
description: Construct, configure, and run the engine; its lifecycle.
---

# The engine (Thresher)

The **Thresher** is gobpm's runtime. One `Thresher` value owns the registry of
process definitions and every running instance; everything else in this manual
executes on top of it. The developer contract is small and lifecycle-shaped:
**construct** the engine (optionally swapping subsystems), **register** a
process (validated once into an immutable launch template), **run** the engine
goroutine under a context, **start** instances, and **observe** or **wait** on
them until they finish. This page is the developer reference for that contract —
the constructor, the option families, the lifecycle methods, and the runtime
behavior you must know. The full quick-start program is
[`examples/basic-process/`](../../../examples/basic-process/).

## Taxonomy

| | |
|---|---|
| Package | `github.com/dr-dobermann/gobpm/pkg/thresher` |
| Type | `thresher.Thresher` (construct via `New`) |
| Owns | the definition registry (versioned launch templates) and every live instance |
| You get back | `*ProcessRegistration` (a version receipt) and `*InstanceHandle` (a read-only instance window) |
| Concurrency | the engine runs concurrently; discovery/observation is safe from any goroutine, state is lock-free |

The `Thresher` is the top of the entity stack — see
[The entity stack](entities.md) for what sits beneath it and
[Architecture overview](architecture.md) for the layer relationship (model →
snapshot → instance → engine).

## Constructor

```go
func New(id string, opts ...Option) (*Thresher, error)
```

| Parameter | Meaning |
|---|---|
| `id` | the engine's id (shown in the startup config, useful when you run several). |
| `opts` | zero or more functional options (below) that swap in subsystems. |

`New` creates an empty engine in the `NotStarted` state and only initializes
inner structures — it does **not** start anything. Every engine-level extension
defaults to its bundled core implementation, so a zero-option `New` produces a
fully working in-memory engine (there is no `NewDefault`); each `WithXxx`
overrides exactly one subsystem. To run it, call `Run`.

## Options

The default engine is fully in-memory and needs no options. Most programs reach
for at most these:

| Option | When you reach for it |
|---|---|
| `WithLogger(l)` | replace the structured logger (default `slog.Default()`). |
| `WithoutBanner()` | silence the ASCII startup banner. |
| `WithoutStartupConfig()` | silence the configuration dump. |
| `WithMessageBroker(b)` | swap the in-memory inbox for your broker. |

The full set. Every option is a `thresher.Option` (`func(*thresherConfig) error`)
passed to `New`; each substitutes one subsystem or tunes a default. Options that
take a subsystem reject a nil argument rather than erasing the working default.

| Option | Effect |
|---|---|
| `WithLogger(l observability.Logger)` | set the structured logger (default `slog.Default()`). |
| `WithTracer(t observability.Tracer)` | set the tracer (default: no-op). |
| `WithMetricsRecorder(m observability.MetricsRecorder)` | set the metrics recorder. |
| `WithMessageBroker(b messaging.MessageBroker)` | set the message broker (default: in-memory inbox). |
| `WithClock(ck clock.Clock)` | set the clock (default: system wall clock). |
| `WithRepository(r repository.Repository)` | set the definition repository (default: in-memory). |
| `WithDataStore(ref string, store datastore.DataStore)` | register an engine-global Data Store under `ref`; call once per store. |
| `WithExpressionEngine(e expression.Engine)` | register an expression engine. |
| `WithoutDefaultExpressionEngines()` | drop the bundled `GoExpr` / `Lite` engines. |
| `WithScriptEngine(e script.Engine)` | register a script engine (none bundled by default). |
| `WithRuleEngine(e rules.Engine)` | register a decision-table / rule engine. |
| `WithAuthorizationProvider(a auth.AuthorizationProvider)` | set the authorization provider (default: allow-all). |
| `WithTaskDistributor(d interactor.TaskDistributor)` | set the human-task distributor. |
| `WithWorkerDispatcher(d tasks.WorkerDispatcher)` | set the external-worker dispatcher. |
| `WithWorkerTrustDefault(mode tasks.TrustMode)` | engine-wide default worker trust mode. |
| `WithWorkerRetryPolicy(p tasks.RetryPolicy)` | engine-wide default worker retry policy. |
| `WithWorkerErrorMapper(m tasks.ErrorMapper)` | engine-wide default worker error mapper. |
| `WithoutBanner()` | suppress the ASCII wordmark / version banner. |
| `WithoutStartupConfig()` | suppress the per-extension configuration dump. |

> Each subsystem option has an extension page in
> [Part 6 — Extending gobpm](../index.md#part-6--extending-gobpm), and the full
> catalog with signatures lives in
> [Engine options catalog](../reference/engine-options.md).

A separate family, `RegisterOption`, is passed to `RegisterProcess` — currently
just `WithManualStart()` (below).

## Lifecycle

The engine's public contract is a handful of methods on `*Thresher`. The core
five drive one process end to end:

| Method | Role |
|---|---|
| `RegisterProcess(p, opts…) (*ProcessRegistration, error)` | validate & snapshot a definition into a versioned launch template. |
| `Run(ctx) error` | bring the engine goroutine and event hub up, tied to `ctx`. |
| `StartLatest(key) (*InstanceHandle, error)` | launch an instance of the newest registered version. |
| `WaitCompletion(ctx)` *(on the handle)* | block until the instance reaches a terminal state. |
| `Shutdown(ctx) error` | gracefully stop the engine, cascade-terminating instances. |

Starting and discovery have more entry points:

| Method | Role |
|---|---|
| `StartVersion(key, version) (*InstanceHandle, error)` | launch a specific 1-based version by `(key, version)`. |
| `StartProcess(reg) (*InstanceHandle, error)` | launch the exact version named by a `*ProcessRegistration` (nil rejected). |
| `Registrations(key) []*ProcessRegistration` | the registered versions of a key, ascending (gaps possible after removals). |
| `Instance(id) (*InstanceHandle, bool)` | the read-only handle of a tracked instance, or `false`. |
| `Instances(filter) []string` | ids of tracked instances (`InstancesAll` / `InstancesRunning` / `InstancesCompleted`). |
| `Forget(ids…) error` | release terminal instances from tracking (all-or-nothing; still-live id rejected). |
| `Observe(o) *Subscription` | subscribe to the engine-wide observation stream. |
| `State() State` | the engine's current lifecycle state (lock-free). |

> `RegisterProcess` is **not** an idempotent no-op: re-registering an existing
> key mints a NEW version (ADR-019). The latest version supersedes for
> auto-instantiation; a superseded version only finishes its already-running
> instances.

## Build it

Create the engine by id — no options needed for the in-memory defaults — build
your process, register it, then start the engine goroutine under a bounded
context:

```go
engine, err := thresher.New("basic-process-engine")
if err != nil {
    return fmt.Errorf("create engine: %w", err)
}

if _, err := engine.RegisterProcess(proc); err != nil {
    return fmt.Errorf("register process: %w", err)
}

ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

if err := engine.Run(ctx); err != nil {
    return fmt.Errorf("run engine: %w", err)
}
```

Launch an instance of the latest registered version and block until it reaches a
terminal state:

```go
h, err := engine.StartLatest(proc.ID())
if err != nil {
    return fmt.Errorf("start process: %w", err)
}

state, err := h.WaitCompletion(ctx)
if err != nil {
    return fmt.Errorf("waiting for completion: %w", err)
}
```

> Call `data.CreateDefaultStates()` once, before building any data-carrying
> elements — it registers the standard data states (`Ready`, `Unavailable`, …)
> that process properties instantiate with. See
> [Item definitions & item-aware elements](../data/item-definitions.md).

## Run it

```bash
cd examples/basic-process && go run .
```

After the startup banner and configuration dump, the engine registers the
definition, comes up, starts one instance, runs the task, and completes
(banner/config lines elided):

```
INFO ProcessLifecycle Registered process_id=7219408995009503683 version=1
INFO EngineState Starting
INFO HubState Started
INFO EngineState Started
INFO InstanceState Created instance_id=6760762898508538721
INFO InstanceState Active instance_id=6760762898508538721
  ▶ hello, dr.Dobermann (instance started at 2026-07-27 09:14:30 …)
INFO InstanceState Completed instance_id=6760762898508538721
✓ basic-process completed (Completed): start → service task → end
```

Silence the two leading blocks with `thresher.New("…", thresher.WithoutBanner(),
thresher.WithoutStartupConfig())`.

## The instance handle

`StartLatest` / `StartVersion` / `StartProcess` all return an
`*InstanceHandle` — a **read-only** window onto one running instance. It wraps
the engine's internal instance by reference but exposes only observation, never
the instance object nor any mutating method, so a host cannot corrupt a running
instance.

| Handle method | Role |
|---|---|
| `WaitCompletion(ctx) (InstanceState, error)` | block until terminal (`Completed`/`Terminated`); backed by a never-dropped done signal — no manual channel or grace sleep. |
| `State() InstanceState` | the instance's current state. |
| `ID() string` | the instance id. |
| `Data() service.DataReader` | read the instance's data by name. |
| `Tokens() []TokenView` | the live token positions. |
| `History() []TokenPath` | the path each token has walked. |
| `Observe(o) *Subscription` | subscribe to this instance's observation stream only. |
| `Cancel(ctx) (InstanceState, error)` | request cancellation and wait for the terminal state. |

`InstanceState` is an **open** vocabulary (ADR-013 §2.4) — the set grows
additively (`Failing`/`Paused`/… as their subsystems land), so a consumer must
tolerate unknown values. The states exercised today are `Created`, `Active`,
`Completed`, `Terminating`, `Terminated`.

## How it works

The five core calls map to five responsibilities:

- **`RegisterProcess`** validates the definition and snapshots it into an
  immutable launch template, assigning a version. Every instance clones that
  template into its own node graph, so registration happens once and launches
  are cheap. It returns a `*ProcessRegistration` receipt naming the
  `(key, version)` — pass it to `StartProcess` or `UnregisterProcess`. By
  default the process is registered for auto-instantiation (a message start
  event with no incoming flow gets a persistent instance-starter);
  `WithManualStart()` opts out, so the process is instantiated only via
  `StartProcess`.
- **`Run(ctx)`** brings the engine goroutine and the event hub up; the engine's
  lifecycle is tied to `ctx`. It transitions `NotStarted → Starting → Started`
  (publishing `Started` only once the hub accepts). Cancelling `ctx` — here the
  5-second timeout — cascade-terminates running instances and unblocks the hub.
- **`StartLatest(key)`** launches one instance of the newest registered version
  and returns its handle. The instance runs its own goroutines (tracks) until an
  end event. `StartVersion` and `StartProcess` pin an older or exact version.
- **`WaitCompletion(ctx)`** blocks until the instance reaches a terminal state,
  returning it. It is backed by the instance's terminal done-channel — a
  guaranteed, never-dropped signal — so it replaces any manual done channel or
  grace sleep.
- **`Shutdown(ctx)`** flips the engine to the terminal `Stopped` state
  (rejecting further `Run`/`RegisterProcess`/`StartProcess`), cancels the engine
  context — cascade-terminating every running instance and unblocking the hub —
  waits (bounded by `ctx`) for each instance to settle, then drains the event
  hub. It is idempotent and returns `ctx.Err()` if the deadline hits first.

The engine's own lifecycle states are a closed set — `NotStarted`, `Starting`,
`Started`, `Paused`, `Stopping`, `Stopped` — read via `State()`, which is a
lock-free atomic load safe to call from any goroutine at any time. `Stopped` is
terminal.

> **Warning:** `Run`, the `Start*` calls, and `WaitCompletion` all take a
> context. Give them one with a deadline (or cancel it yourself) — a bare
> `context.Background()` will let `WaitCompletion` block forever if an instance
> never terminates.

## Observing the engine

`engine.Observe(o)` registers an `observability.Observer` on the **engine-wide**
stream: it receives every engine-kind event and every running instance's events
(each carrying `instance_id` in its details). The `InstanceHandle.Observe`
variant scopes the same contract to one instance. Both return a `*Subscription`;
delivery is buffered, lossy, and drop-counted — `OnFact` runs on a per-observer
drain goroutine off the engine's execution path, may block without stalling the
engine (Facts past the buffer are dropped, count them with `Subscription.Dropped()`),
and a panic in it is recovered. Cancel the subscription to stop.

```go
type Observer interface {
    OnFact(Fact)
}
```

For the fact model, reporters, and the operator log, see
[Observability](observability.md) and
[Observability in practice](../operating/observability.md).

## See also

- Full example: [`examples/basic-process/`](../../../examples/basic-process/)
- Getting started: [Your first process](../getting-started/first-process.md) · [Running & observing](../getting-started/running-and-observing.md)
- Runtime: [Process, instance, track, token](execution-model.md) · [How a process executes](process-execution.md) · [Observability](observability.md)
- Controlling: [Registering & versioning](../operating/registering-and-versioning.md) · [Starting instances](../operating/starting-instances.md) · [Instance lifecycle](../operating/instance-lifecycle.md)
- Reference: [Engine options catalog](../reference/engine-options.md)
- Design: [ADR-013 — instance observability](../../design/ADR-013-instance-observability.md) · [ADR-019 — definition versioning](../../design/ADR-019-definition-versioning.md) · [ADR-002 — extension architecture](../../design/ADR-002-extension-architecture.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/thresher`
