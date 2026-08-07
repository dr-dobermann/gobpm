---
title: Compensation events
description: Undo completed work in reverse order.
---

# Compensation events

**Compensation** undoes work that already **completed successfully** — the saga
pattern in BPMN form. You reach for it when a later step fails and there is no
transaction to roll back: each finished activity carries an *undo* handler
(a task marked `isForCompensation`, tied to the activity by a **Compensation
boundary**), and **throwing** compensation replays those handlers in **reverse
completion order** to walk the scope back. This page is the developer reference
for the three moving parts — the event definition, the boundary that arms a
handler, and the throw trigger.

## Taxonomy

| | |
|---|---|
| BPMN category | Event → Compensation (Start · Intermediate catch/throw · End) (§10.4.5) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/events` |
| Definition type | `events.CompensationEventDefinition` (a `flow.EventDefinition`) |
| Trigger constant | `flow.TriggerCompensation` (`CompensationEventDefinition.Type()`) |
| Boundary type | `events.BoundaryEvent`, built by `NewCompensationBoundaryEvent` |
| Carried on | an `events.EndEvent` or throw event via `WithCompensationTrigger` |
| The work | route completed activities to their `WithCompensation()` undo handlers |

Where it sits in the event family: [Events taxonomy](index.md). It rides on the
[Boundary events](boundary.md) machinery and pairs with the
[Transaction Sub-Process](../subprocesses/transaction.md), which throws
compensation automatically on Cancel.

## Constructor

Compensation is three collaborating pieces. Most sagas wire all three:

| Constructor | Builds |
|---|---|
| `NewCompensationEventDefinition(activity, waitForCompletion, …)` | the shared definition every compensation event carries. |
| `NewCompensationBoundaryEvent(name, host, def, handler, …)` | the boundary that arms an undo handler on a completed activity. |
| `WithCompensationTrigger(ced)` | the throw — put it on an `EndEvent` (or throw event) to replay the handlers. |

The definition is the shared piece — every compensation event carries one:

```go
func NewCompensationEventDefinition(
    activity flow.ActivityNode,
    waitForCompletion bool,
    baseOpts ...options.Option,
) (*CompensationEventDefinition, error)
```

| Parameter | Meaning |
|---|---|
| `activity` | the single activity to compensate, or `nil` to compensate the **whole enclosing scope** (§13.5.5, the spec's default target context). |
| `waitForCompletion` | when `true`, the throw blocks until every handler finishes before the token moves on. |
| `baseOpts` | base-element options (id, docs). |

It returns an error, never panics. Per the spec (§10.4.5) the definition has
four placements: a Compensation Start (event sub-process only), catch and throw
Intermediate events, and a Compensation End event.

## Arming a handler — the Compensation boundary

An undo handler never sits in the normal flow; it is reached only through a
Compensation boundary attached to the completed activity:

```go
func NewCompensationBoundaryEvent(
    name string,
    host flow.ActivityNode,
    def *CompensationEventDefinition,
    handler flow.ActivityNode,
    baseOpts ...options.Option,
) (*BoundaryEvent, error)
```

| Parameter | Meaning |
|---|---|
| `name` | the boundary event's diagram name. |
| `host` | the activity whose completion this boundary compensates. |
| `def` | a `CompensationEventDefinition` (typically `nil` activity). |
| `handler` | the undo handler — **must** be marked `WithCompensation()`. |

It validates every parameter: non-nil `host`, `def`, and `handler`, and rejects
a `handler` that is not `isForCompensation`. Unlike an interrupting boundary,
there is nothing to cancel — the guarded activity already completed — so a
Compensation boundary takes no `cancelActivity` flag (the stored value stays the
spec default, `true`).

## Throwing compensation

The throw is an ordinary event carrying a compensation trigger. On an
`EndEvent` (or a throw event) use `WithCompensationTrigger`:

```go
func WithCompensationTrigger(ced *CompensationEventDefinition) EventOption
```

A `nil` activity on the definition means **compensate the enclosing scope,
scope-wide** — every completed activity that has a handler. Pass a specific
`flow.ActivityNode` to target just one.

> The undo handler must be marked `activities.WithCompensation()`. That flag
> lifts the task out of the normal flow, so it never runs on the happy path —
> only when compensation is thrown. `NewCompensationBoundaryEvent` rejects an
> unmarked handler, since the boundary is the only path that reaches it.

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/events`.

## Build it

An **undo handler** is a Service Task marked `WithCompensation()`:

```go
st, _ := activities.NewServiceTask(name, op,
    activities.WithoutParams(), activities.WithCompensation())
```

A **Compensation boundary** ties a completed activity to its handler:

```go
ced, _ := events.NewCompensationEventDefinition(nil, true)
be, _ := events.NewCompensationBoundaryEvent(name, host, ced, handler)
```

The **throw** is an End Event carrying the compensation trigger; a `nil`
activity compensates the enclosing scope:

```go
ced, _ := events.NewCompensationEventDefinition(nil, true)
cancelTrip, _ := events.NewEndEvent("cancel-trip",
    events.WithCompensationTrigger(ced))
```

Only the normal-flow nodes are linked with sequence flows — the undo handlers
are reached through their boundaries, never wired into the happy path:

```go
for _, l := range [][2]flow.Element{
    {start, hotel},
    {hotel, flight},
    {flight, cancelTrip},
} {
    flow.Link(l[0].(flow.SequenceSource), l[1].(flow.SequenceTarget))
}
```

## Run it

`examples/compensation-events/` is a trip-booking saga: `book-hotel` then
`book-flight` both succeed, then a Compensation End Event undoes the whole scope.

```bash
cd examples/compensation-events && go run .
```

Both bookings complete, then the undo handlers run **flight first, hotel
second**, and the instance completes:

```
  ✓ hotel booked
  ✓ flight booked
  ↩ flight booking canceled
  ↩ hotel booking canceled
InstanceState Completed instance_id=…

✓ compensation-events completed (Completed): both bookings entered the
  completion ledger; the Compensation End Event undid them in reverse order
  and waited for both handlers
```

## Methods & runtime behavior

The definition and boundary expose these; the engine drives them — you rarely
call them directly:

| Method | Role |
|---|---|
| `CompensationEventDefinition.Activity() flow.ActivityNode` | the targeted activity, or `nil` for scope-wide. |
| `CompensationEventDefinition.WaitForCompletion() bool` | whether the throw blocks on its handlers. |
| `CompensationEventDefinition.Type() flow.EventTrigger` | reports `flow.TriggerCompensation`. |
| `BoundaryEvent.CompensationHandler() flow.ActivityNode` | the undo handler this boundary routes to. |
| `BoundaryEvent.AttachedTo() flow.ActivityNode` | the host activity guarded by the boundary. |

Behavior worth knowing:

- **Only completed work compensates.** Each activity that finishes enters the
  engine's **completion ledger**. A step that never completed is not in the
  ledger, so nothing is undone for it — the spec's *presumed-abort*: no
  completion, no compensation.
- **Reverse completion order.** The throw replays ledger entries newest-first,
  so the last thing done is the first undone: `undo-flight` before `undo-hotel`.
- **Handlers read a snapshot.** Each handler sees the data its activity
  completed with — captured at completion, not the live scope at undo time. The
  handler's own writes still go to the live scope.
- **`waitForCompletion`.** With `true`, the throw blocks until every handler
  returns before the token moves on; the instance completes only after both
  undos finish.
- **An empty throw is logged, never a fault.** If nothing is in scope to
  compensate, the throw resolves to nothing, logs it, and continues — never
  silently dropped, never an error.

## Options & variations

- **Scope-wide vs targeted throw.** A `nil` activity on the throw definition
  compensates the whole enclosing scope; a specific `flow.ActivityNode`
  compensates just that one activity.
- **Intermediate throw.** The example throws from an End Event, but a
  Compensation intermediate throw event compensates mid-flow and lets the run
  continue afterwards.
- **Transaction Sub-Process.** A transaction's Cancel path compensates its
  completed activities automatically — the same ledger and reverse-order
  machinery, driven by a Cancel rather than a manual throw. See
  [Transaction Sub-Process](../subprocesses/transaction.md).

## Restarts

A **resolving sweep survives the checkpoint**: restored mid-run, the
remaining queue continues in reverse completion order, the handler
that was RUNNING re-runs (a handler is an effect — at-least-once, and
well-defined because it reads an immutable snapshot), the
already-compensated entries never re-run, and a wait-for-completion
thrower resumes only once the sweep drains.


## See also

- Examples: `examples/compensation-events/`
- Related guides: [Error](error.md) · [Boundary events](boundary.md) · [Transaction Sub-Process](../subprocesses/transaction.md) · [Service Task](../tasks/service-task.md)
- Design: [ADR-026 — Compensation events](../../design/ADR-026-compensation-events.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/events`
