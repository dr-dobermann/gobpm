---
title: Message events
description: Send and receive messages across instances.
---

# Message events

A **message** is a named, payload-carrying signal routed through the engine's
MessageBroker from one point in a process to another — possibly across two
different instances. You reach for a message when one place must **publish**
data and another must **wait** for it and bind that payload into its own scope.
gobpm exposes the message trigger in two shapes: the **event** shape
(`IntermediateThrowEvent` / `IntermediateCatchEvent` carrying a
`MessageEventDefinition`) and the task shape ([`SendTask` /
`ReceiveTask`](../tasks/send-receive-task.md)). This page is the reference for
the event shape — the trigger definition, its constructor and options, the
carrier events, and the runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Event → trigger → **Message** (`MessageEventDefinition`) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/events` |
| Trigger type | `events.MessageEventDefinition` (`Type()` → `flow.TriggerMessage`) |
| Message carrier | `bpmncommon.Message` — a name + an `ItemDefinition` payload |
| Carried by | catch/throw intermediate events (this page), plus Start / End / Boundary |
| Throw event | `events.IntermediateThrowEvent` — publishes to the broker |
| Catch event | `events.IntermediateCatchEvent` — waits on a MessageWaiter, binds the payload |
| Implements | `MessageEventDefinition` → `flow.EventDefinition` (`Type`, `GetItemsList`) |

Where it sits in the event family: [Events taxonomy](index.md). The task-shaped
peer is on [Send / Receive Task](../tasks/send-receive-task.md).

## The Message carrier

A message pairs a **name** (the routing key) with an `ItemDefinition` (the
payload shape). Both endpoints reference a `Message` with the **same name** —
that name is what the broker matches on. The item IDs on each side name the
*local* data the payload is read from and bound into; they need not match each
other.

```go
func MustMessage(
    name string,
    item *data.ItemDefinition,
    baseOpts ...options.Option,
) *Message
```

| Parameter | Meaning |
|---|---|
| `name` | the message name — the broker routing key (`"order placed"`). |
| `item` | the payload's `ItemDefinition`; its ID names the local data bound. |
| `baseOpts` | base-element options (id, documentation). |

`NewMessage` is the error-returning twin; `MustMessage` panics on an invalid
combination. Accessors: `Name() string`, `Item() *data.ItemDefinition`.

## Constructor

The trigger is a `MessageEventDefinition` wrapping a `Message`:

```go
func NewMessageEventDefinition(
    msg *bpmncommon.Message,
    operation service.Operation,
    baseOpts ...options.Option,
) (*MessageEventDefinition, error)
```

| Parameter | Meaning |
|---|---|
| `msg` | the `bpmncommon.Message` to publish or wait for. Required — a nil `msg` is rejected. |
| `operation` | an optional `service.Operation` associated with the message (WSDL-style pairing); pass `nil` for the plain broker round-trip. |
| `baseOpts` | base-element options. |

It returns an error — never panics — when `msg` is nil. `MustMessageEventDefinition`
is the panic-on-error twin used in the examples.

You then hand the definition to a carrier event:

```go
func NewIntermediateThrowEvent(name string, def flow.EventDefinition, baseOpts ...options.Option) (*IntermediateThrowEvent, error)
func NewIntermediateCatchEvent(name string, def flow.EventDefinition, baseOpts ...options.Option) (*IntermediateCatchEvent, error)
```

## Options

The message trigger takes base-element options only; the *event* it is attached
to takes the event options. Most message events need none of them — the trigger
and its carrier event are enough.

| Option | When you reach for it |
|---|---|
| (none) | a plain send/receive round-trip — just the message name + payload item. |

The full event-option family (`events.EventOption`) lets a **catch** event carry
more than one trigger, or pins the message to a correlation key. These are wired
through `WithMessageTrigger` when you build a multi-trigger catch event rather
than passing a single definition to the constructor:

| Event option | Effect |
|---|---|
| `WithMessageTrigger(med *MessageEventDefinition)` | add a message trigger to an event's `eventConfig` (multi-trigger catch). |
| `WithCorrelationKey(key *bpmncommon.CorrelationKey)` | route by a correlation key so the message reaches the right instance — see [Correlation](../operating/correlation.md). |
| `WithIterationCorrelation(keyName, expr)` | route to the right **iteration** when a parallel Multi-Instance body waits at this catch: `keyName` names a declared process correlation key (its retrieval derives the envelope-side value), `expr` evaluates at registration over the iteration's scope (the split item is bound there). Without it, a second concurrent waiter on one definition is refused loudly — delivery would be ambiguous. |
| `WithInterrupting()` / `WithNonInterrupting()` | for a **boundary** message event — see [Boundary events](boundary.md). |

> The single-definition constructors above (`NewIntermediateCatchEvent(name,
> med)`) are the common path; reach for `WithMessageTrigger` only when an event
> catches several trigger kinds at once.

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/events`.

## The EventDefinition contract

`MessageEventDefinition` satisfies `flow.EventDefinition` — the interface every
carrier event drives:

```go
type EventDefinition interface {
    foundation.Identifyer
    foundation.Documentator
    Type() EventTrigger              // returns flow.TriggerMessage
    GetItemsList() []*data.ItemDefinition
}
```

You rarely implement it — you construct a `MessageEventDefinition` and pass it
to a carrier. Its message-specific accessors are `Message() *bpmncommon.Message`
and `Operation() service.Operation`.

## Build it

A throw event publishes; a catch event waits and binds. Both name the same
`"order placed"` message; the throw reads its payload from `order_out`, the
catch binds the arriving payload into `order_in`. From
`examples/message-intermediate-events/`:

```go
throw, err := events.NewIntermediateThrowEvent("throw-order",
    events.MustMessageEventDefinition(
        bpmncommon.MustMessage("order placed",
            data.MustItemDefinition(values.NewVariable(""),
                foundation.WithID("order_out"))),
        nil))

catch, err := events.NewIntermediateCatchEvent("catch-order",
    events.MustMessageEventDefinition(
        bpmncommon.MustMessage("order placed",
            data.MustItemDefinition(values.NewVariable(""),
                foundation.WithID("order_in"))),
        nil))
```

Wire the nodes on one track (`start → throw → catch → confirm → end`) so the
throw completes before the catch subscribes; the in-memory broker buffers the
published message until then. A downstream service reads the bound payload by ID:

```go
got, err := r.GetDataByID("order_in")
```

## Run it

```bash
cd examples/message-send-receive && go run .
```

Past the startup banner, the message travels the broker and the bound payload
verifies (task-shaped peer, same round-trip):

```
  ✓ send-order published "ORD-2026-001"
  ✓ receive-order bound it into received-order = "ORD-2026-001"
✓ message-demo completed: the message travelled the broker from the SendTask to the ReceiveTask
```

The event-shaped example (`examples/message-intermediate-events/`) does the
identical round-trip with throw/catch events instead of send/receive tasks.

## Methods & runtime behavior

The engine drives the carrier events through these — you rarely call them:

| Method | Role |
|---|---|
| `IntermediateThrowEvent.Exec` | publish the message to the broker, then continue. |
| `IntermediateThrowEvent.MessageToSend()` | the `bpmncommon.Message` this throw publishes. |
| `IntermediateCatchEvent.Exec` | register the MessageWaiter and **park** the track until arrival. |
| `IntermediateCatchEvent.ProcessEvent` | on delivery, accept the event definition. |
| `IntermediateCatchEvent.UploadData` | bind the arriving payload into scope. |
| `MessageEventDefinition.Type()` | `flow.TriggerMessage` — how the hub classifies it. |

Behavior worth knowing:

- **Name is the route.** The broker matches subscribers by the message name; the
  per-side item IDs (`order_out`, `order_in`) name only local data.
- **The catch side parks.** A catch event (like a ReceiveTask) subscribes a
  MessageWaiter and parks its track until the message arrives, then binds the
  payload before emitting its outgoing flows. With a repository configured the
  instance goes further and **dehydrates**: the engine takes over the
  subscription — keyed to the instance's conversation — and the instance holds
  no goroutines at all until a message wakes it
  ([Persistence & recovery](../operating/persistence.md)).
- **Buffered delivery.** The in-memory broker buffers a published message, so a
  throw that fires *before* the matching catch subscribes still delivers — the
  single-track ordering (throw then catch) relies on this.
- **Across instances.** The broker is engine-global; a throw in one instance and
  a catch in another exchange the message the same way. When more than one
  instance could receive, use [correlation](../operating/correlation.md) to route
  it to the right one.

## See also

- Examples: `examples/message-send-receive/` (task shape) · `examples/message-intermediate-events/` (event shape)
- Related guides: [Send / Receive Task](../tasks/send-receive-task.md) · [Signal](signal.md) · [Timer](timer.md) · [Correlation & conversations](../operating/correlation.md) · [How events are processed](../concepts/event-processing.md)
- Design: [ADR-014 — Message handling](../../design/ADR-014-message-handling.md) · [ADR-016 — Message correlation](../../design/ADR-016-message-correlation.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/events`
