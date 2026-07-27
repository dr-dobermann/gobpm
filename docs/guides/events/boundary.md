---
title: Boundary events
description: Arm an event on an activity; interrupt or run alongside it.
---

# Boundary events

A **boundary event** is a catch event attached to the edge of an activity. It
arms when the activity starts and watches for its trigger — a timer, a message,
a signal, an error, an escalation, a conditional — while the activity runs. When
it fires it routes a token onto its own **exception flow**, either *interrupting*
the activity (cancelling it) or firing *alongside* it (non-interrupting). This is
how you put a timeout on a long step, catch a thrown error, or react to an
escalation without touching the activity's own code. This page is the developer
reference — the type, its two constructors, the trigger rules, and its runtime
behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Event → Catch → **Boundary Event** (§10.5.4) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/events` |
| Type | `events.BoundaryEvent` — one type, parameterized by its trigger definition (SRD-029 §4.1) |
| Attaches to | a `flow.ActivityNode` — any task, sub-process, or call activity |
| Implements | `flow.BoundaryEvent` (`BoundTo`, `AttachedTo`, `CancelActivity`), `flow.Node`, `eventproc.EventProcessor` (`ProcessEvent`) |
| The trigger | a `flow.EventDefinition` (timer, message, signal, error, escalation, conditional, compensation) — the behavior lives in the definition, not a type hierarchy |

Where it sits in the event family: [Events taxonomy](index.md).

## Constructor

The trigger behavior is carried by the `EventDefinition`; the boundary is one
type. The `cancelActivity` flag is the interrupting/non-interrupting choice.

```go
func NewBoundaryEvent(
    name string,
    host flow.ActivityNode,
    def flow.EventDefinition,
    cancelActivity bool,
    baseOpts ...options.Option,
) (*BoundaryEvent, error)
```

| Parameter | Meaning |
|---|---|
| `name` | the boundary's diagram name (and default id source). |
| `host` | the activity it guards — any `flow.ActivityNode` (task, sub-process, call activity). |
| `def` | the trigger — a `flow.EventDefinition` (timer, message, signal, error, escalation, conditional). |
| `cancelActivity` | `true` interrupts the host on fire; `false` runs alongside it (non-interrupting). |
| `baseOpts` | zero or more base options (e.g. `foundation.WithID`). |

It returns an error — never panics — on any invalid argument: a nil `host` or
`def`, a trigger not allowed on a boundary, or a **non-interrupting Error
boundary** (an Error boundary is always interrupting — BPMN §10.5.6). For a
message trigger the payload output is registered so an arrived payload binds into
scope on resume.

Compensation is a distinct shape — it routes to a handler activity rather than an
exception flow, so it has its own constructor:

```go
func NewCompensationBoundaryEvent(
    name string,
    host flow.ActivityNode,
    def *CompensationEventDefinition,
    handler flow.ActivityNode,
    baseOpts ...options.Option,
) (*BoundaryEvent, error)
```

The `handler` must be marked `isForCompensation` (it lives off the normal flow
and runs only when compensation is thrown). There is no `cancelActivity`
argument — the guarded activity has already completed, so there is nothing to
interrupt — see [Compensation](compensation.md).

## Options

A boundary event takes no interrupting/parallel *options* — the interrupting
choice is the `cancelActivity` positional argument, and the trigger is the `def`
argument. The only `baseOpts` you typically pass is an explicit id:

| Option | When you reach for it |
|---|---|
| `foundation.WithID(id)` | pin a stable id instead of deriving one from `name`. |

> The package-level `events.WithInterrupting()` / `events.WithNonInterrupting()`
> / `events.WithParallel()` options configure **event sub-processes** and catch
> events, not the boundary — the boundary's interrupting choice is the
> `cancelActivity` bool. Don't reach for them here.

The trigger is chosen by which `EventDefinition` you build and pass as `def`:

| Trigger | Definition constructor | Notes |
|---|---|---|
| Timer | `NewTimerEventDefinition(tDate, tCycle, tDuration)` | fires a duration/instant after activity entry. |
| Message | `NewMessageEventDefinition(msg, operation, …)` | payload binds into scope on resume. |
| Signal | `NewSignalEventDefinition(signal)` | broadcast catch. |
| Error | `NewErrorEventDefinition(cErr)` | always interrupting (§10.5.6). See [Error](error.md). |
| Escalation | `NewEscalationEventDefinition(escalation)` | interrupting or not. See [Escalation](escalation.md). |
| Conditional | `NewConditionalEventDefinition(condition)` | fires on a false→true edge. See [Conditional](conditional.md). |
| Compensation | `NewCompensationEventDefinition(activity, waitForCompletion)` | use `NewCompensationBoundaryEvent`. |

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/events`.

## The BoundaryEvent contract

`events.BoundaryEvent` implements `flow.BoundaryEvent` — the attachment interface
the engine uses to arm and route it. You don't implement this yourself; the
engine and the constructor call it. It is worth knowing what it exposes:

```go
type BoundaryEvent interface {
    EventNode

    BoundTo(ActivityNode) error   // attach; enforces multiplicity
    AttachedTo() ActivityNode     // the guarded activity (nil until BoundTo)
    CancelActivity() bool         // interrupting (true) vs non-interrupting (false)
}
```

`BoundTo` enforces the **multiplicity rule**: at most one interrupting handler
per Event Declaration on a given activity (ADR-018 §2.5); non-interrupting
handlers are unbounded. The constructor wires the host, so you normally call
`proc.Add` and `flow.Link` rather than `BoundTo` directly.

## Build it

A boundary needs the **host activity**, an **event definition** (the trigger),
and the **interrupting** bool. Build the host first, then the boundary; the
example arms a 2-second interrupting timer on a ~4s payment task:

```go
def, err := events.NewTimerEventDefinition(when, nil, nil)
be, err := events.NewBoundaryEvent(id, host, def, true) // interrupting
```

Add the boundary to the process like any element, then `flow.Link` its
**exception** flow to the handler. The host's own outgoing flow is the *normal*
path — the two paths out of the activity:

```go
for _, e := range []flow.Element{
    start, payment, cancelOrder, endPaid, endCancelled, boundary,
} {
    proc.Add(e)
}

for _, l := range [][2]flow.Element{
    {start, payment},          // normal path in
    {payment, endPaid},        // normal path out
    {boundary, cancelOrder},   // exception path out
    {cancelOrder, endCancelled},
} {
    flow.Link(l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget))
}
```

For the interruption to take effect promptly the host must **honour its
context** — the payment op `select`s on `ctx.Done()` and returns early:

```go
select {
case <-time.After(4 * time.Second):
    return nil, nil                    // finished normally
case <-ctx.Done():
    fmt.Println("  ✗ process-payment: interrupted before it finished")
    return nil, ctx.Err()              // boundary fired: bail out
}
```

## Run it

```bash
cd examples/boundary-events && go run .
```

The 2s timer beats the 4s payment; the engine cancels the payment track,
discards its result, and routes to cancellation (banner elided):

```
  → process-payment: charging the card (takes ~4s)...
  ✗ process-payment: interrupted before it finished
  → cancel-order: payment timed out, releasing the reservation

✓ boundary-events completed (Completed): the 2s timer boundary fired before the
4s payment finished — it interrupted the activity and routed to cancel-order
```

## Methods & runtime behavior

The engine drives the boundary through these — you rarely call them directly:

| Method | Role |
|---|---|
| `BoundTo(host)` / `AttachedTo()` | attach to / read the guarded activity (constructor wires this). |
| `CancelActivity()` | interrupting (`true`) vs non-interrupting (`false`). |
| `ProcessEvent(ctx, def)` | capture a fired definition's payload for binding on resume. |
| `CompensationHandler()` | the handler a Compensation boundary routes to (nil otherwise). |
| `IsParallelMultiple()` | whether the boundary may fire more than once (non-interrupting). |
| `EventClass()` / `Clone()` | introspection; per-instance copy. |

Behavior worth knowing:

- **Arm on entry, disarm on completion.** The boundary arms when the token
  arrives on its host and disarms when the activity completes normally — a
  timer's clock starts from activity entry, not process start.
- **Interrupting fire cancels the host.** The activity's op sees `ctx.Done()`,
  returns early, and its result is thrown away by the interruption checkpoint —
  the normal outgoing flow is never taken (ADR-018 §2.2).
- **Non-interrupting fires alongside.** With `cancelActivity=false` the activity
  keeps running and a *parallel* token is emitted on the exception flow; it may
  fire more than once (ADR-018 §2.3).
- **Honour the context.** A host that ignores `ctx` runs to completion before the
  cancellation is observed — interruption is prompt only if the work is
  cancellable.
- **Two distinct paths out.** Give the host a real normal flow *and* the boundary
  its own exception flow; exactly one is taken per interrupting run.

## See also

- Examples: `examples/boundary-events/` (interrupting timer boundary)
- Related guides: [Timer](timer.md) · [Error](error.md) · [Escalation](escalation.md) · [Conditional](conditional.md) · [Compensation](compensation.md) · [Event sub-processes](event-subprocess.md)
- Design: [ADR-018 — Boundary events and activity interruption](../../design/ADR-018-boundary-events-and-activity-interruption.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/events`
