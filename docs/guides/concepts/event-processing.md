---
title: How events are processed
description: The EventHub, waiters, correlation, and delivery to waiting instances.
---

# How events are processed

BPMN work rarely runs start-to-finish in a straight line: a Receive Task waits
for a message, a boundary event arms a timer, a signal fans out to every
listener. The engine piece that makes those *waits* work is the **EventHub** —
the central router between elements that **produce** events and elements that
**wait** on them. This page explains the observable behavior — register, park,
deliver, resume — and the two public contracts a node implements to take part
(`EventProcessor`, `EventProducer`). The hub and the waiters themselves are
`internal/` and are described here only as behavior, never as an API you call.

## The two public contracts

Everything a *node* needs lives in one small package, `pkg/eventproc`. A node
that catches an event implements `EventProcessor`; the thing it registers on is
an `EventProducer`. The hub, the waiters, and their goroutines are internal
(ADR-012 v.1) — you never construct them.

| Interface | Package | You implement it when… | Members |
|---|---|---|---|
| `EventProcessor` | `pkg/eventproc` | your node catches a fired event | `foundation.Identifyer` + `ProcessEvent(ctx, flow.EventDefinition) error` |
| `EventProducer` | `pkg/eventproc` | (engine-side) you register/propagate events | `RegisterEvent`, `UnregisterEvent`, `PropagateEvent` |

The processor side is the one most code meets — the built-in catching elements
(Receive Task, intermediate catch, boundary event) already implement it:

```go
type EventProcessor interface {
    foundation.Identifyer

    // ProcessEvent processes a single event definition by the node it is
    // registered for in an EventProducer.
    ProcessEvent(context.Context, flow.EventDefinition) error
}
```

The producer side is the registration + propagation surface. A catching node
hands *itself* (as an `EventProcessor`) plus the `flow.EventDefinition` it is
waiting for to `RegisterEvent`; a throwing node calls `PropagateEvent` to send a
fired throw-event's definition up the chain:

```go
type EventProducer interface {
    RegisterEvent(EventProcessor, flow.EventDefinition) error
    UnregisterEvent(ep EventProcessor, eDefID string) error
    PropagateEvent(context.Context, flow.EventDefinition) error
}
```

Match is by **event-definition id**: once the producer sees an event whose
definition id matches a registered one, it calls the registered processor with
that definition. That id is the single join point behind every catch.

## The lifecycle: register → park → deliver → resume

Under every catching element the hub runs a **waiter** in its own goroutine. The
observable sequence is always the same four steps:

1. **Register.** When a catching node activates, it calls the hub's registration
   with itself (an `EventProcessor`) and its event definition. The hub creates a
   **waiter** keyed by that definition and starts its service goroutine.
2. **Park.** The node's track releases its goroutine and waits — it does not spin.
   A parked instance holds no CPU; it resumes only on delivery.
3. **Deliver.** When a matching event fires — a broker delivery, a clock tick, a
   signal broadcast — the waiter fires and reports back to the hub, which invokes
   the registered processor's `ProcessEvent` with the fired definition.
4. **Resume.** `ProcessEvent` binds any payload into scope and un-parks the
   track, which runs on to its outgoing flows.

> A waiter never removes itself. It sets its own state and reports the fire to
> the hub; the hub — the sole owner of waiter removal — drops a single-shot
> waiter that reached a terminal state and keeps a still-running one (a
> persistent message subscription, or a timer mid-cycle). This single-owner rule
> is why concurrent instances can share the plumbing without racing.

Each waiter moves through a small state machine (`WSCreated → WSReady →
WSRunned → WSEnded`, or `WSStopped` / `WSFailed`). You never observe these
directly — they exist so the hub can tell a terminal waiter (remove it) from a
live one (keep it).

## Delivery shapes: one vs. all

The same register/park/deliver/resume loop backs every catching event, but the
*fan-out* differs by event kind — this is the behavior a developer must know:

| Event kind | Waiter | Fires on | Fan-out |
|---|---|---|---|
| Message | message waiter | a broker delivery matching the subscription | **one** subscriber (routed, correlated) |
| Signal | signal waiter | a broadcast of the matching signal name | **every** waiting catcher at once |
| Timer | time waiter | a clock tick | the one waiter that armed it |

A **message** is point-to-point and correlated: the broker delivers it to the
one subscription whose keys match, buffering it in a bounded inbox if nobody is
waiting yet, and preferring a **most-specific** key-set (a correlation key over a
bare name) so a follow-up routes to the conversation that owns it. A **signal**
has no correlation — one throw broadcasts to *all* catchers in reach. A **timer**
fires on the clock rather than on a delivery.

## Message delivery — a worked path

The `message-send-receive` example wires a Send Task and a Receive Task on **one
track**: the send publishes to the broker, the receive parks on a waiter and
binds the payload on arrival. Because both are on the same track, the send
completes *before* the receive subscribes — the in-memory broker buffers the
envelope until the subscription appears, so the send-then-receive still connects.

Run it for real:

```bash
cd examples/message-send-receive && go run .
```

```
  ✓ send-order published "ORD-2026-001"
  ✓ receive-order bound it into received-order = "ORD-2026-001"
✓ message-demo completed: the message travelled the broker from the SendTask to the ReceiveTask
```

The two `✓` lines are the whole path: the Send Task published the `order placed`
message, the waiter under the Receive Task fired, `ProcessEvent` bound the
payload into scope, and the track resumed to land it in the `received-order`
Data Object. (The engine's startup banner and per-state `INFO` lines above them
are the operator log — filter them to see the two result lines.)

## Signal delivery — broadcast

The `signal-broadcast` example shows the *all*-fan-out: two independent
`watcher` instances each park on an intermediate catch of the signal
`order-cancelled`; a single `canceller` throws it once, and **both** watchers
resume. A signal carries no correlation — catcher and thrower each build their
own `SignalEventDefinition` (distinct nodes) and meet by signal **name**, and
one throw fires every matching waiter (ADR-006). Full program:
[`examples/signal-broadcast/`](../../../examples/signal-broadcast/).

## Throwing an event

A catch parks and waits; a throw *propagates*. A throwing node calls
`PropagateEvent` on its producer, which sends the fired throw-event's
`flow.EventDefinition` up the chain of producers to whichever registered
processors are waiting on that definition id. If no catcher is registered, the
propagation is a **no-op** — a signal thrown into the void simply reaches nobody,
it is not an error.

## Behavior worth knowing

- **Parking is real release, not a spin.** A waiting instance holds no CPU; the
  track goroutine is released until delivery — and with a repository configured
  a fully-idle instance releases **every** goroutine, its loop included, waking
  from its checkpoint ([Dehydration](../operating/persistence.md)). This is the single-writer
  execution model of ADR-017 — cross-goroutine event delivery is funnelled so
  each instance still has one logical writer.
- **The hub owns removal.** Single-shot waiters are removed after they fire;
  persistent waiters (event-triggered instance-starters) and mid-cycle timers
  are kept. You never remove a waiter yourself.
- **Buffering is bounded.** The broker's inbox holds an undelivered message
  until a subscription appears, but drops the oldest past its cap — uncorrelated
  messages can't grow without bound.
- **Match is by definition id.** Registration, delivery, and propagation all key
  off the `flow.EventDefinition` id; two nodes meet only if their definitions
  share it (for signals/messages, that is the name; for correlated messages, the
  key-set).

## See also

- Examples: [`examples/message-send-receive/`](../../../examples/message-send-receive/) · [`examples/signal-broadcast/`](../../../examples/signal-broadcast/)
- Related guides: [How a process executes](process-execution.md) · [Message](../events/message.md) · [Signal](../events/signal.md) · [Timer](../events/timer.md) · [Correlation & conversations](../operating/correlation.md)
- Design: [ADR-006 — Events & subscriptions](../../design/ADR-006-events-and-subscriptions.md) · [ADR-017 — Channel-based event processing](../../design/ADR-017-channel-based-event-processing.md) · [ADR-014 — Message handling](../../design/ADR-014-message-handling.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/eventproc`
