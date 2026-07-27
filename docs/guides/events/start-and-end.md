---
title: Start & End events
description: How instances begin and finish.
---

# Start & End events

Every process needs a way in and a way out. A **start event** is the
instantiation point — the engine emits a token from it when an instance begins.
An **end event** is a completion point — when a token arrives, that branch is
done. In their plainest form both are one-line constructors that carry no
trigger and simply bracket the flow; each also accepts trigger options to react
to (start) or emit (end) a BPMN event. This page is the developer reference:
the two types, their constructors, every option, and their runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Event → **Start Event** (§10.5.2) · **End Event** (§10.5.6) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/events` |
| Type | `events.StartEvent` · `events.EndEvent` |
| Embeds | `StartEvent` embeds a catch `Event`; `EndEvent` embeds a throw `Event` (both carry `flow.BaseNode`, `Definitions`, `Triggers`, `Properties`) |
| Implements | both: `flow.Node`, `flow.EventNode` (`EventClass`, `Definitions`), `exec.NodeExecutor` (`Exec`) · start also `flow.SequenceSource`, `exec.NodeDataProducer` · end also `flow.SequenceTarget`, `exec.NodeDataConsumer` |
| The work | start: emit a token on the outgoing flows · end: consume the token, emit/terminate on any trigger |

Where they sit in the event family: [Events taxonomy](index.md).

## Constructors

```go
func NewStartEvent(name string, startEventOptions ...options.Option) (*StartEvent, error)
func NewEndEvent(name string, endEventOptions ...options.Option)     (*EndEvent, error)
```

| Parameter | Meaning |
|---|---|
| `name` | the event's diagram name (and default id source). |
| `startEventOptions` / `endEventOptions` | zero or more options (below) — a bare start/end takes none. |

Both return an error — never panic — on an invalid combination (for example a
trigger the event class does not accept: an end event rejects a timer/conditional
trigger, a start event rejects a `Cancel`/`Terminate` trigger).

## Options

A plain start and a plain end need **no options at all** — the pair below is the
whole common surface:

| Option | When you reach for it |
|---|---|
| *(none)* | a bare start/end that just brackets the flow. |
| `WithTerminateTrigger(ted)` | end that tears down the **whole** instance, not just its branch. |
| `foundation.WithID(id)` | pin a stable id instead of deriving one from the name. |

The full set groups by which event accepts it. **Shared** options (both events)
come from `foundation` and `data`:

| Shared option | Effect |
|---|---|
| `foundation.WithID(id string)` | set a stable id. |
| `foundation.WithDoc(text, format string)` | attach documentation. |
| `data.WithProperties(props ...*data.Property)` | declare event-local properties. |

**Start-event trigger options** — the start reacts to the trigger. Accepted
triggers (`startTriggers`): Message, Signal, Timer, Conditional, Error,
Escalation, Compensation. A `Cancel` or `Terminate` trigger is rejected:

| Start trigger option | Effect |
|---|---|
| `WithMessageTrigger(med)` | instantiate on an incoming message. |
| `WithSignalTrigger(sed)` | instantiate on a broadcast signal. |
| `WithTimerTrigger(ted)` | instantiate on a timer (date/cycle/duration). |
| `WithConditionalTrigger(ced)` | start on a data condition (event sub-process). |
| `WithErrorTrigger(eed)` · `WithEscalationTrigger(eed)` · `WithCompensationTrigger(ced)` | event-sub-process / in-line sub-process starts. |

**Start-event configuration options** — shape a start that already carries a
trigger; they do not add a trigger:

| Start config option | Effect |
|---|---|
| `WithParallel()` | mark a multiple start "parallel" (all triggers must fire to instantiate). |
| `WithInterrupting()` | event-sub-process start interrupts its parent scope — the default (§13.5.4), explicit documentation. |
| `WithNonInterrupting()` | event-sub-process start runs concurrently with the parent; not valid with an Error trigger (§10.5.6). |
| `WithCorrelationKey(key)` | the `bpmncommon.CorrelationKey` an instantiating message start correlates on — see [Correlation & conversations](../operating/correlation.md). |

**End-event trigger options** — the end **emits** (or acts on) the trigger.
Accepted triggers (`endTriggers`): Terminate, Cancel, Error, Escalation,
Message, Signal, Compensation:

| End trigger option | Effect |
|---|---|
| `WithTerminateTrigger(ted)` | terminate the whole instance; the instance settles `Terminated`. |
| `WithErrorTrigger(eed)` | end the process in error, faulting the instance with the error code (§10.5.6). |
| `WithCancelTrigger(ced)` | abort the enclosing Transaction Sub-Process (§10.7) — valid only inside a transaction. |
| `WithEscalationTrigger(eed)` · `WithMessageTrigger(med)` · `WithSignalTrigger(sed)` · `WithCompensationTrigger(ced)` | throw the corresponding event as the branch ends. |

> Each trigger has its own guide with the definition constructor and semantics —
> see [Message](message.md), [Signal](signal.md), [Timer](timer.md),
> [Error](error.md), [Escalation](escalation.md), [Conditional](conditional.md),
> [Terminate](terminate.md), [Compensation](compensation.md).

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/events`.

## Build it

Both constructors are one line; wire them into the process like any other flow
node — the start feeds the first node, the last node feeds the end:

```go
start, err := events.NewStartEvent("start")
// ...
end, err := events.NewEndEvent("end")
// ...
for _, e := range []flow.Element{start, task, end} {
    proc.Add(e)
}
flow.Link(start, task)
flow.Link(task, end)
```

The engine drives the full lifecycle around the pair:

```go
engine.RegisterProcess(proc)           // definition → launch template
engine.Run(ctx)                        // engine goroutine comes up
h, _ := engine.StartLatest(proc.ID())  // instance emits a token from the start
state, _ := h.WaitCompletion(ctx)      // returns when tokens reach the ends
```

## Run it

From [`examples/basic-process/`](../../../examples/basic-process/) — start →
service task → end. After the startup banner and config dump the instance starts
at the start event, runs the task, and settles at the end event as `Completed`:

```
2026/07/27 09:17:46 INFO InstanceState Created instance_id=4817251568700577704
2026/07/27 09:17:46 INFO InstanceState Active instance_id=4817251568700577704
  ▶ hello, dr.Dobermann (instance started at 2026-07-27 09:17:46.86 …)
2026/07/27 09:17:46 INFO InstanceState Completed instance_id=4817251568700577704
✓ basic-process completed (Completed): start → service task (read property + RUNTIME var) → end
```

**Terminate end event — end the whole instance at once.** Attach a terminate
trigger and reaching that end tears down the entire instance, cancelling any
in-flight branches (their operation contexts are cancelled). The instance
settles `Terminated`, not `Completed`:

```go
termEd, _ := events.NewTerminateEventDefinition()
terminate, _ := events.NewEndEvent("terminate-order",
    events.WithTerminateTrigger(termEd))
```

From [`examples/terminate-end-event/`](../../../examples/terminate-end-event/) —
one branch flags fraud and terminates the instance mid-payment:

```
  ⚠ fraud-check: fraudulent order detected — terminating the process
  → process-payment: charging the card (takes ~3s)...
  ✗ process-payment: interrupted before it finished
```

## Methods & runtime behavior

The engine drives both through these — you rarely call them directly:

| Method | Role |
|---|---|
| `Exec(ctx, re) ([]*flow.SequenceFlow, error)` | start: return the outgoing flows (emit the token). End: consume the token; emit its triggers, or `Terminate`/`Cancel`/fault on Terminate/Cancel/Error. |
| `EventClass()` | `flow.StartEventClass` / `flow.EndEventClass`. |
| `Definitions()` / `Triggers()` | inspect the event's trigger definitions. |
| `Clone()` | per-instance copy (config shared by reference, fresh flows). |
| `SupportOutgoingFlow` (start) / `AcceptIncomingFlow` (end) | flow-wiring guards — start takes no incoming, end takes no outgoing. |
| `IsInterrupting()` (start) | whether an event-sub-process start interrupts its parent. |

Behavior worth knowing:

- **Start** has no incoming sequence flow; `StartLatest` places a token on its
  outgoing flow and execution proceeds. A trigger start (message/signal/timer)
  instantiates when its trigger fires.
- **End** is a completion point, not the instance's death. Each token that
  reaches a plain end ends *its own* branch; a process may hold **several** end
  events and settles `Completed` once *every* live token has reached one. A
  `Terminate` end tears the whole instance down at once (`re.Terminate()`); a
  `Cancel` end aborts the enclosing Transaction; an `Error` end faults the
  instance with the error code. Terminate/Cancel are checked before the emit
  loop, so they win over a co-located trigger.

> A plain end event ends only the branch that reached it. If other branches are
> still running, the instance keeps going until they too reach an end.

## See also

- Examples: [`examples/basic-process/`](../../../examples/basic-process/) · [`examples/terminate-end-event/`](../../../examples/terminate-end-event/)
- Related guides: [Your first process](../getting-started/first-process.md) · [Terminate](terminate.md) · [Boundary events](boundary.md) · [Event sub-processes](event-subprocess.md) · [Correlation & conversations](../operating/correlation.md)
- Design: [ADR-006 — events and subscriptions](../../design/ADR-006-events-and-subscriptions.md) · [ADR-016 — message correlation](../../design/ADR-016-message-correlation.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/events`
