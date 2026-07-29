---
title: Starting instances
description: Launch an instance and get its handle.
---

# Starting instances

A registered process is a *definition*, not a running thing. To make it run you
**start an instance** — the engine clones the definition's launch snapshot into
its own live node graph and drives a token through it. Every start returns an
`InstanceHandle`: a read-only window onto that one run, the thing you wait on,
observe, and cancel. This page covers the three start methods, how they differ,
and what the handle gives you back.

> The engine must be running before you start anything. Call `Run(ctx)` first —
> a start on a stopped engine has no scheduler to drive the token. See
> [The engine (Thresher)](../concepts/engine.md).

## The three ways to start

You always start a *registered* process (see [Definition
versioning](versioning.md)). What differs is how you name the version to launch:

| Method | Names the version by | Reach for it when |
|---|---|---|
| `StartLatest(key)` | the process key — newest version | "just run the current one" (the common case). |
| `StartVersion(key, version)` | key + a 1-based version | you must re-run a specific older version by number. |
| `StartProcess(reg)` | the `ProcessRegistration` receipt | you already hold the receipt `RegisterProcess` returned and want that exact version. |

All three return `(*InstanceHandle, error)` and launch a **fresh** instance
every call — starting is never idempotent.

```go
func (t *Thresher) StartLatest(key string) (*InstanceHandle, error)
func (t *Thresher) StartVersion(key string, version int) (*InstanceHandle, error)
func (t *Thresher) StartProcess(reg *ProcessRegistration) (*InstanceHandle, error)
```

Error contract — each rejects bad input instead of launching a broken run:

| Method | Errors when |
|---|---|
| `StartLatest` | `key` is empty, or no version is registered for it. |
| `StartVersion` | `key` is empty, `version < 1`, or no such key/version is registered. |
| `StartProcess` | `reg` is nil. |

`StartProcess` pins the exact `(key, version)` the receipt names, so a later
re-registration (which mints a new latest version) never shifts what it
launches. `StartLatest` always follows the newest version. The `key` is the
process key you registered under — in the basic example it is `proc.ID()`.

## The registration receipt

`RegisterProcess` returns a `*ProcessRegistration` — the receipt that identifies
the exact version you just registered. It is read-only (identity only; never the
engine-internal snapshot), and it is what `StartProcess` addresses:

| Method | Returns |
|---|---|
| `ID()` | the registration's own id. |
| `Key()` | the process key it registered under. |
| `Version()` | the 1-based version number. |

> By default a registered process is available both for auto-instantiation (a
> message start event spawns instances) and for explicit `StartProcess`.
> `RegisterProcess(p, thresher.WithManualStart())` opts out of
> auto-instantiation — the process then starts **only** via the start methods
> above. See [Definition versioning](versioning.md).

## Start it

From `examples/basic-process/` — register, run the engine, start the latest,
then wait on the handle:

```go
reg, err := engine.RegisterProcess(proc)
if err != nil {
    return fmt.Errorf("register process: %w", err)
}

if err := engine.Run(ctx); err != nil {
    return fmt.Errorf("run engine: %w", err)
}

h, err := engine.StartLatest(proc.ID())
if err != nil {
    return fmt.Errorf("start process: %w", err)
}

state, err := h.WaitCompletion(ctx)
```

Running the example (banner and config elided):

```
InstanceState Created   instance_id=5819936164817998804
InstanceState Active    instance_id=5819936164817998804
  ▶ hello, dr.Dobermann (instance started at 2026-07-27 11:16:50 …)
InstanceState Completed instance_id=5819936164817998804
✓ basic-process completed (Completed): start → service task → end
```

The instance walks `Created → Active → Completed`; `WaitCompletion` unblocks at
the terminal state and returns it.

## The instance handle

`InstanceHandle` is a read-only window onto one running instance — it wraps the
engine's internal instance by reference but exposes only observation, so a host
can never corrupt a running run. The methods you reach for most:

| Method | What it gives you |
|---|---|
| `ID() string` | the instance id (matches the `instance_id` in the log). |
| `WaitCompletion(ctx) (InstanceState, error)` | block until terminal (`Completed`/`Terminated`) or `ctx` done. |
| `State() InstanceState` | the current lifecycle state, sampled now. |
| `Cancel(ctx) (InstanceState, error)` | request termination of the instance. |

The full observation and control surface:

| Method | Role |
|---|---|
| `Observe(o Observer) *Subscription` | subscribe to this instance's fact stream. |
| `Data() service.DataReader` | read the instance's current data by name. |
| `Tokens() []TokenView` | snapshot of the live tokens and their positions. |
| `History() []TokenPath` | the path tokens have walked so far. |
| `Suspend(ctx) error` / `Resume(ctx) error` | pause and resume the instance. |

`WaitCompletion` is the one to build on: it is backed by the instance's terminal
done-channel close — a guaranteed, never-dropped signal — unlike the lossy
observation stream. It returns the terminal state observed and the fatal error
that stopped the instance (or `ctx.Err()` on timeout/cancel). For everything the
handle does after the start — waiting, cancelling, inspecting state and tokens —
see [Instance lifecycle](instance-lifecycle.md).

## Instance states

`InstanceState` is an **open** vocabulary — tolerate unknown values, the set
grows additively as new subsystems land:

| State | Meaning |
|---|---|
| `StateCreated` | instance built, not yet advancing. |
| `StateActive` | a token is advancing. |
| `StateCompleted` | reached a normal end (terminal). |
| `StateTerminating` | tearing down after a cancel / terminate-end. |
| `StateTerminated` | stopped before normal completion (terminal). |
| `StateDehydrated` | idle with **no goroutines**, waiting on held waits (not terminal — a trigger rebuilds it). |

## See also

- Examples: `examples/basic-process/`
- Related guides: [Registering & versioning](versioning.md) · [Instance lifecycle](instance-lifecycle.md) · [The engine (Thresher)](../concepts/engine.md)
- Design: [ADR-013 — instance observability](../../design/ADR-013-instance-observability.md) · [ADR-019 — definition versioning](../../design/ADR-019-definition-versioning.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/thresher`
