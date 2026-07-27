---
title: Signal events
description: Broadcast to every listener; signal start.
---

# Signal events

A **signal** is a fire-and-forget broadcast: one throw reaches *every* catcher
in range at once. Unlike a message, a signal has **no correlation** and no
single addressee — thrower and catchers meet only by the signal **name**. Reach
for a signal when one event must fan out to many independent listeners
("order cancelled", "system shutting down", "batch ready"), or to start every
process registered on that name. This page is the developer reference — the
types, their constructors, the trigger option, the contract the definition
satisfies, and the runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Event → definition → **Signal** (§10.5.4) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/events` |
| Types | `events.Signal`, `events.SignalEventDefinition` |
| Embeds | `Signal` embeds `foundation.BaseElement`; `SignalEventDefinition` embeds the shared definition base |
| Implements | `SignalEventDefinition` satisfies `flow.EventDefinition` (`Type`, `GetItemsList`) plus `CheckItemDefinition` |
| The trigger | `flow.TriggerSignal` — carried by a catch / throw / boundary / start event that references the definition |

Where it sits in the event family: [Events taxonomy](index.md).

## Constructors

A signal is built in two steps: a **`Signal`** (the named thing, with an
optional payload item) and a **`SignalEventDefinition`** (the trigger a node
carries).

```go
func NewSignal(
    name string,
    str *data.ItemDefinition,
    baseOpts ...options.Option,
) (*Signal, error)

func NewSignalEventDefinition(
    signal *Signal,
    baseOpts ...options.Option,
) (*SignalEventDefinition, error)
```

| Parameter | Meaning |
|---|---|
| `name` | the signal's name — the only thing thrower and catcher match on. |
| `str` | optional payload item (`*data.ItemDefinition`); `nil` for a payload-less signal. |
| `signal` | the `*Signal` this definition triggers on. `nil`/empty is rejected. |
| `baseOpts` | zero or more base-element options (id, docs). |

Both return an error, never panic: `NewSignal` on an empty name,
`NewSignalEventDefinition` on a nil signal. A `Must…` variant panics instead of
returning the error, for package-level definitions:

```go
func MustSignalEventDefinition(
    signal *Signal,
    baseOpts ...options.Option,
) *SignalEventDefinition
```

> Catcher and thrower each build their **own** `SignalEventDefinition` — they
> are distinct nodes that meet by **name**, not by sharing one object. There is
> no shared definition to align.

## The trigger option

The definition is not a node by itself; an event node carries it. An
intermediate catch or throw node takes it as its `def` argument; a **start** or
**boundary** event takes it via the `WithSignalTrigger` option:

```go
func WithSignalTrigger(sed *SignalEventDefinition) EventOption
```

| Carrier | Constructor | How the signal is attached |
|---|---|---|
| Intermediate Catch | `NewIntermediateCatchEvent(name, def, …)` | `def` = the `*SignalEventDefinition` |
| Intermediate Throw | `NewIntermediateThrowEvent(name, def, …)` | `def` = the `*SignalEventDefinition` |
| Start Event | `NewStartEvent(name, WithSignalTrigger(sed))` | via the trigger option |
| Boundary Event | `NewBoundaryEvent(name, host, def, cancelActivity, …)` | `def` = the `*SignalEventDefinition` |

A trigger not allowed for the chosen carrier is rejected by the constructor.

## The EventDefinition contract

`SignalEventDefinition` is what a node reads to know *what* it waits on or
throws. It satisfies `flow.EventDefinition`, so any event machinery that handles
definitions generically handles a signal:

```go
type EventDefinition interface {
    foundation.Identifyer
    foundation.Documentator
    Type() EventTrigger
    GetItemsList() []*data.ItemDefinition
}
```

`Type()` returns `flow.TriggerSignal`; `GetItemsList()` reports the signal's
payload item (empty when the signal is payload-less) so an arrived payload can
bind into scope on resume. You do not implement this — the constructors return a
ready value — but knowing the contract explains how the engine treats every
event definition uniformly.

## Build it

Wrap the signal in a small helper — each node calls it to get its own
definition:

```go
func signalDef(name string) (*events.SignalEventDefinition, error) {
    sig, err := events.NewSignal(name, nil)
    if err != nil {
        return nil, fmt.Errorf("create signal: %w", err)
    }

    return events.NewSignalEventDefinition(sig)
}
```

The catcher parks on an **Intermediate Catch Event** until the signal arrives;
the thrower broadcasts via an **Intermediate Throw Event**:

```go
catchDef, _ := signalDef(signal)
catch, _ := events.NewIntermediateCatchEvent("await-"+signal, catchDef)

throwDef, _ := signalDef(signal)
throw, _ := events.NewIntermediateThrowEvent("raise-"+signal, throwDef)
```

Each node is wired into a plain `start → node → end` process and registered.
Two watcher instances start and park; one canceller throws once — a single
throw wakes both:

```go
h1, _ := engine.StartLatest(catcher.ID()) // parks on the catch
h2, _ := engine.StartLatest(catcher.ID()) // parks on the catch
engine.StartLatest(thrower.ID())          // one broadcast wakes both
```

## Run it

Running `examples/signal-broadcast/` — two watchers parked on one signal, woken
by a single throw:

```
  ▶ two watcher instances are waiting on "order-cancelled"
  ▶ one canceller threw the signal once
  ✓ watcher 1 completed (Completed) — caught the broadcast
  ✓ watcher 2 completed (Completed) — caught the broadcast
✓ one throw → every waiting instance caught it (broadcast)
```

## Signal start

A Start Event can carry a signal trigger. It has no incoming flow — a broadcast
of the signal *creates* an instance, with no `StartLatest`/`StartProcess` call.
Register the process, then propagate the signal:

```go
def, _ := signalDef(signalName)
start, _ := events.NewStartEvent("start", events.WithSignalTrigger(def))
// …register the process, run the engine, then:
engine.PropagateEvent(ctx, def) // one broadcast instantiates every listener
```

Every registered process that starts on that name gets its own instance.
Because signal-born instances have no start handle, discover them via
`engine.Instances(…)` / `engine.Instance(id)` to await completion. Running
`examples/signal-start/`:

```
  ▶ broadcasting "order-received" once (no StartProcess call)...
  order-received → handling fulfillment
  order-received → recording audit
✓ one broadcast signal created and completed both instances
```

## Methods & runtime behavior

You rarely call these — the engine reads them while matching and delivering. The
few you might touch when introspecting a built model:

| Method | Role |
|---|---|
| `Signal.Name()` | the match key; catcher and thrower agree on this string. |
| `SignalEventDefinition.Type()` | the `flow.EventTrigger` — `TriggerSignal`. |
| `SignalEventDefinition.Signal()` | the underlying `*Signal`. |

The full set:

| Method | Role |
|---|---|
| `Signal.Name()` | the match key thrower and catcher agree on. |
| `Signal.Item()` | the optional payload item (`nil` if payload-less). |
| `SignalEventDefinition.Signal()` | the underlying `*Signal`. |
| `SignalEventDefinition.Type()` | the trigger — `flow.TriggerSignal`. |
| `SignalEventDefinition.GetItemsList()` | the payload items reported for scope binding. |
| `SignalEventDefinition.CheckItemDefinition(id)` | whether the definition is based on the item with `id`. |

Behavior worth knowing:

- **Match by name, not by object.** The engine routes a throw to every catcher
  whose signal *name* matches — no correlation key to compute or align.
- **Broadcast, not point-to-point.** A single throw is delivered to *all*
  reachable catchers, even across independent instances. A [message](message.md),
  by contrast, is consumed by exactly one receiver.
- **Catchers must be waiting.** A signal is transient — it wakes catchers
  parked *at throw time*. A catch reached after the throw keeps waiting; there
  is no replay or buffering.
- **Payload is optional.** `NewSignal(name, nil)` carries none; pass a non-nil
  item to attach one, exposed through `GetItemsList` for scope binding on resume.

> A signal broadcasts to *all* listeners with no correlation. When you need to
> route an event to one specific instance — by a key such as an order id — use a
> [Message](message.md) instead.

## See also

- Examples: `examples/signal-broadcast/` (broadcast) · `examples/signal-start/` (signal start)
- Related guides: [Message](message.md) · [Start & End](start-and-end.md) · [Boundary events](boundary.md) · [How events are processed](../concepts/event-processing.md)
- Design: [ADR-006 — events & subscriptions](../../design/ADR-006-events-and-subscriptions.md) · [ADR-015 — event-triggered instantiation](../../design/ADR-015-event-triggered-instantiation.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/events`
