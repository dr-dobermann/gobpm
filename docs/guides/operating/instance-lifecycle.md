---
title: Instance lifecycle
description: Wait for completion, cancel, and inspect an instance.
---

# Instance lifecycle

Starting a process (`StartLatest` / `StartVersion` / `StartProcess`) hands you an
`*thresher.InstanceHandle` — a **read-only window** onto one running instance. It
wraps the engine's internal instance by reference but exposes only observation
and coarse, engine-mediated control: you can wait for it to finish, request its
cancellation, read its current state, and inspect where its tokens are — but you
can never reach the instance object itself or any mutating method, so a host
cannot corrupt a running instance. This page is the developer reference for that
handle — its lifecycle states, and every method you drive it with.

## The lifecycle states

An instance moves through the `thresher.InstanceState` vocabulary — a
standard-named, **open** set (treat unknown values gracefully; the set grows
additively as deferred subsystems land):

| State | Meaning |
|---|---|
| `StateCreated` | the instance object exists; execution has not begun. |
| `StateActive` | tokens are moving — the instance is running. |
| `StateCompleted` | every branch reached a normal end — a terminal state. |
| `StateTerminating` | termination requested (e.g. a Terminate End Event, or `Cancel`); the ctx-cancel cascade is unwinding in-flight work. |
| `StateTerminated` | the instance stopped abnormally — a terminal state. |
| `StateDehydrated` | the instance is waiting with **no goroutines** — it released them while every track was parked on a held, dehydratable wait, and its checkpoint is the wake source. **Not terminal**: a trigger rebuilds it and it returns to `StateActive`. |

The two **terminal** states are `StateCompleted` and `StateTerminated`; both
`WaitCompletion` and `Cancel` block until the instance settles into one of them.
`StateDehydrated` is **not** one of them: an instance that released its
goroutines has not finished anything, so `WaitCompletion` keeps blocking across
as many dehydration/hydration cycles as the instance goes through. A handle
taken before a release keeps speaking for the instance after it is rebuilt.
State is read lock-free.

> The vocabulary is intentionally open: `Failing` / `Paused` / `Compensating`
> join as their subsystems land, with no breaking change. Switch on the states
> you handle and keep a default branch. The design rationale is in
> [ADR-013 — Instance observability](../../design/ADR-013-instance-observability.md)
> (§2.4) and [ADR-001 — Execution model](../../design/ADR-001-execution-model.md)
> (§4.2).

## Handle methods

Most hosts need only three:

| Method | When you reach for it |
|---|---|
| `WaitCompletion(ctx)` | block until the instance finishes, then read the outcome. |
| `Cancel(ctx)` | request termination and block until it settles. |
| `State()` | poll the current lifecycle state without blocking. |

The full surface — waiting, control, and inspection:

| Method | Role |
|---|---|
| `ID() string` | the instance id. |
| `State() InstanceState` | current lifecycle state; lock-free, non-blocking. |
| `WaitCompletion(ctx) (InstanceState, error)` | block until terminal (or ctx done); return the observed state and the fatal error that stopped it. |
| `Cancel(ctx) (InstanceState, error)` | request termination, block until terminal (or ctx done); return the observed state. Idempotent. |
| `Data() service.DataReader` | read-only reader over the instance's process properties and runtime variables. |
| `Tokens() []TokenView` | snapshot of live token positions — one per active track. |
| `History() []TokenPath` | every track's recorded path, including finished/merged tracks, with fork lineage and per-step timings. |
| `Observe(o Observer) *Subscription` | subscribe to the instance's Fact stream (best-effort, lossy) — see [Observability in practice](observability.md). |
| `Suspend(ctx) error` | reserved — returns `ErrNotImplemented` until the Paused subsystem lands. |
| `Resume(ctx) error` | reserved — counterpart of `Suspend`; returns `ErrNotImplemented`. |

## Waiting for completion

`WaitCompletion` is the primary way to join a running instance. It blocks until
the instance reaches a terminal state (`Completed` or `Terminated`) or `ctx` is
done, returning the state observed and the fatal error that stopped the instance
(or `ctx.Err()` on timeout/cancel):

```go
func (h *InstanceHandle) WaitCompletion(
    ctx context.Context,
) (InstanceState, error)
```

It is backed by the instance's terminal done-channel close — a **guaranteed,
never-dropped signal** (ADR-013 §2.2), unlike the lossy observation stream. So
even if you attach no observer, `WaitCompletion` will always see the end.

From `examples/basic-process/` — start, wait, report the terminal state:

```go
h, err := engine.StartLatest(proc.ID())
if err != nil {
    return fmt.Errorf("start process: %w", err)
}

state, err := h.WaitCompletion(ctx)
if err != nil {
    return fmt.Errorf("waiting for completion: %w", err)
}

fmt.Printf("✓ basic-process completed (%s): "+
    "start → service task (read property + RUNTIME var) → end\n", state)
```

Running it (banner and config dump elided):

```
InstanceState Created instance_id=1834117760924194839
InstanceState Active instance_id=1834117760924194839
  ▶ hello, dr.Dobermann (instance started at 2026-07-27 11:16:56 …)
InstanceState Completed instance_id=1834117760924194839
✓ basic-process completed (Completed): start → service task (read property + RUNTIME var) → end
```

The instance walks `Created → Active → Completed`, and `WaitCompletion` returns
`StateCompleted`.

## Cancelling

`Cancel` requests termination and blocks until the instance reaches a terminal
state or `ctx` is done, returning the observed state (`+ ctx.Err()` on timeout):

```go
func (h *InstanceHandle) Cancel(ctx context.Context) (InstanceState, error)
```

It is **coarse, engine-mediated** control (ADR-013 §2.3): it drives the
instance's ctx-cancel cascade — in-flight work sees a cancelled context and
unwinds — never a back door into the instance's internals. `Cancel` is
**idempotent**: a second call, or a `Cancel` of an already-terminal instance,
returns the terminal state at once. A cancelled instance settles in
`StateTerminated`.

> The same abnormal end happens without a host call when a branch reaches a
> **Terminate End Event** — the engine terminates the whole instance and it
> settles in `Terminated` (not `Completed`). See the `terminate-end-event`
> example and [Terminate](../events/terminate.md). From the outside a
> host-driven `Cancel` and an internal terminate are indistinguishable: both end
> in `StateTerminated`.

`Suspend` / `Resume` are the reserved pause counterparts — the methods exist so
the control contract is stable, but they return `thresher.ErrNotImplemented`
until the deferred Paused subsystem lands. Don't build on them yet.

## Inspecting

Three read-only views let you see inside a running (or finished) instance without
touching it:

| View | Shape | What it tells you |
|---|---|---|
| `Data()` | `service.DataReader` | current process properties and runtime variables (read-only — the reader has no mutator). |
| `Tokens()` | `[]TokenView` | live positions — one entry per active track, each `{NodeID, NodeName, State}`. |
| `History()` | `[]TokenPath` | the full recorded path of every track (active and finished), with fork lineage and per-step timings. |

A `TokenView` reports where a token currently sits and its `TokenState` — `Alive`
(moving) or `WaitForEvent` (parked). `Tokens()` is the live-active snapshot;
`History()` is the "including merged tokens" view, where finished tracks (ended,
merged, canceled) project to a `Consumed` terminal and merged tracks carry a
`MergedInto` survivor edge. Each `TokenPath` holds a `Steps []StepVisit` trail,
each visit stamped with `At time.Time`, the node, and the projected state. Both
inspection calls are lock-free (copy-on-write snapshots), so they're safe to call
concurrently against a live instance.

For continuous observation rather than point-in-time polling, attach an
`Observer` with `Observe` — covered in
[Observability in practice](observability.md).

## Finding a handle again

If you didn't keep the handle from `Start*`, ask the engine for it by id:

```go
h, ok := engine.Instance(instanceID)
```

`Thresher.Instance` returns the same read-only `*InstanceHandle` (and `false` if
no such instance is known). `Thresher.Instances(filter)` lists instance ids by an
`InstanceFilter` (`InstancesAll` and the running/terminal filters). These are the
discovery accessors — starting and registering processes live on the
[Starting instances](starting-instances.md) page.

## See also

- Examples: `examples/basic-process/` (wait for completion) · `examples/terminate-end-event/` (abnormal termination)
- Related guides: [Starting instances](starting-instances.md) · [Observability in practice](observability.md) · [The engine (Thresher)](../concepts/engine.md) · [Terminate](../events/terminate.md)
- Design: [ADR-013 — Instance observability](../../design/ADR-013-instance-observability.md) · [ADR-001 — Execution model](../../design/ADR-001-execution-model.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/thresher`
