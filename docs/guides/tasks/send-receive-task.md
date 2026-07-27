---
title: Send & Receive Tasks
description: Message-driven tasks: publish a message, wait for one.
---

# Send & Receive Tasks

Two task types move a **message** across a process boundary through the
engine's `MessageBroker`. A **Send Task** publishes a message and completes; a
**Receive Task** parks until a matching message arrives, binds its payload into
scope, then completes. They are the task-shaped peers of the throwing and
catching [message events](../events/message.md) — reach for a task when the
send/receive is the step's whole job, for an event when it decorates a boundary
or a start. This page is the developer reference — the two types, their
constructors, options, and runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Activity → Task → **Send Task** / **Receive Task** (§10.3.6) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/activities` |
| Types | `activities.SendTask`, `activities.ReceiveTask` |
| Inherits | the `Activity` attributes — I/O sets, boundary events, loop characteristics, data associations |
| Send implements | `flow.Node`, `exec.NodeExecutor` (`Exec`), the data phases (`LoadData`/`UploadData`), `msgflow.MessageProducer` (`MessageToSend`) |
| Receive implements | the above **plus** `flow.EventNode` (`Definitions`, `EventClass`) and `eventproc.EventProcessor` (`ProcessEvent`) — a Receive Task is a wait node |
| The work | a `bpmncommon.Message` — the payload published (Send) or awaited (Receive) |

Where they sit in the activity family: [Activities taxonomy](index.md).

## Constructors

Both take the task name, the `*bpmncommon.Message`, and options. A nil message
is rejected — never a panic.

```go
func NewSendTask(
    name string,
    msg *bpmncommon.Message,
    taskOpts ...options.Option,
) (*SendTask, error)

func NewReceiveTask(
    name string,
    msg *bpmncommon.Message,
    taskOpts ...options.Option,
) (*ReceiveTask, error)
```

| Parameter | Meaning |
|---|---|
| `name` | the task's diagram name (and default id source). |
| `msg` | the message to publish (Send) or wait for (Receive) — a `*bpmncommon.Message`. Nil is rejected. |
| `taskOpts` | zero or more options (below). |

The message itself is built with `bpmncommon.NewMessage(name, item, …)` (or the
panicking `MustMessage`); its `item` is the `data.ItemDefinition` whose id names
the scope property the payload is read from (Send) or bound into (Receive).

## Options

Most message tasks need only a data-declaration option — the same activity
options every task carries:

| Option | When you reach for it |
|---|---|
| `WithoutParams()` | the task moves the payload by the message's item id, no declared I/O (Send's common case). |
| `WithParameters(dir, params…)` | declare a typed `data.Output` so the received payload lands in a named parameter (Receive's common case). |
| `WithInstantiate()` | *(Receive only)* make an incoming-flow-less Receive Task start a new instance on the message — the task-shaped message start. |

The full set comes from the shared **activity options** plus one **type-specific
option** per task:

| Activity option | Effect |
|---|---|
| `WithParameters(d data.Direction, params ...*data.Parameter)` | declare typed inputs/outputs. |
| `WithoutParams()` | declare no parameters. |
| `WithCompensation()` | mark the task a compensation handler (armed, off the normal flow). |
| `WithLoop(lc)` / `WithMultyInstance()` | repeat the activity — see [Standard Loop](../iteration/standard-loop.md), [Multi-Instance](../iteration/multi-instance.md). |
| `WithStartQuantity(n)` / `WithCompletionQuantity(n)` | BPMN token quantities (default 1). |

| Type-specific option | Task | Effect |
|---|---|---|
| `WithCorrelationKey(key *bpmncommon.CorrelationKey)` | Send (`SndTaskOption`) | derive the key from the payload and stamp it on the published envelope so a keyed consumer can correlate (ADR-016 §2.2). Nil key = name-match only. |
| `WithInstantiate()` | Receive (`RcvTaskOption`) | mark the task instantiating — a flow-less, `instantiate=true` Receive Task starts a new instance on a matching message (BPMN §13.3.3), like a message start event. |

> Boundary events are attached with the method `AddBoundaryEvent`, not a
> constructor option — see [Boundary events](../events/boundary.md).

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/activities`.

## The Message

Both tasks carry a `bpmncommon.Message` — the unit that travels the broker:

```go
func NewMessage(name string, item *data.ItemDefinition, baseOpts ...options.Option) (*Message, error)
func MustMessage(name string, item *data.ItemDefinition, baseOpts ...options.Option) *Message

func (m Message) Name() string
func (m Message) Item() *data.ItemDefinition
```

The **name** is what a Receive Task matches on; the **item** carries the
payload — its `foundation.WithID` names the scope property the Send Task reads
from and the Receive Task binds into. Send and Receive typically share the
message name (`"order placed"`) but each carries its own item id (`order_out`
on the sender, `order_in` on the receiver), so the payload crosses the broker
by name and lands under the receiver's local id.

## Build it

A Send Task publishes a bound property; a Receive Task waits and binds the
arriving payload into a declared output. From `examples/message-send-receive`:

```go
send, _ := activities.NewSendTask("send-order",
    bpmncommon.MustMessage("order placed",
        data.MustItemDefinition(values.NewVariable(""),
            foundation.WithID("order_out"))),
    activities.WithoutParams())

outParam := data.MustParameter("received order",
    data.MustItemAwareElement(
        data.MustItemDefinition(values.NewVariable(""),
            foundation.WithID("order_in")),
        data.UnavailableDataState))

receive, _ := activities.NewReceiveTask("receive-order",
    bpmncommon.MustMessage("order placed",
        data.MustItemDefinition(values.NewVariable(""),
            foundation.WithID("order_in"))),
    activities.WithParameters(data.Output, outParam))
```

## Run it

Both tasks live on one track, so the send completes before the receive
subscribes — the in-memory broker buffers the message until then. Running
`examples/message-send-receive/`:

```
  ✓ send-order published "ORD-2026-001"
  ✓ receive-order bound it into received-order = "ORD-2026-001"
✓ message-demo completed: the message travelled the broker from the SendTask to the ReceiveTask
```

## Methods & runtime behavior

The engine drives both tasks through these — you rarely call them directly:

| Method | Task | Role |
|---|---|---|
| `Exec(ctx, re) ([]*flow.SequenceFlow, error)` | both | Send: publish, then return the outgoing flows. Receive: bind the captured payload on resume, then return the flows. |
| `MessageToSend()` / `Message()` | Send | the message the task publishes. |
| `ExpectedMessage()` / `Message()` | Receive | the message the task waits for. |
| `Definitions()` / `EventClass()` | Receive | the single `MessageEventDefinition` the track registers to park and subscribe. |
| `ProcessEvent(ctx, eDef)` | Receive | capture the arrived payload on fire (as an `EventProcessor`). |
| `Instantiate()` | Receive | whether the task starts a new instance on the message. |
| `CorrelationKey()` | Send | the declared correlation key (or nil). |
| `LoadData` / `UploadData` | both | bind declared inputs before, commit outputs after. |
| `AddBoundaryEvent(be)` / `BoundaryEvents()` | both | attach / inspect boundary events. |

Behavior worth knowing:

- A **Send Task** publishes synchronously in `Exec` and completes at once — once
  the message is on the broker, the task is done (BPMN §10.3.6).
- A **Receive Task** is a wait node: the track registers its message event
  definition, **parks** (releases its goroutine) in `TrackWaitForEvent` while a
  `MessageWaiter` subscribes the broker, and resumes only when a matching
  message fires — then `Exec` binds the captured payload into scope (ADR-014).
- With `WithInstantiate()` and no incoming flow, a Receive Task *starts* a
  process on a matching message, exactly like a message start event.
- Correlation beyond name-matching (routing a message to the right instance) is
  keyed via `WithCorrelationKey` on the sender — see [Correlation &
  conversations](../operating/correlation.md).

## See also

- Examples: `examples/message-send-receive/`
- Related guides: [Message events](../events/message.md) · [Service Task](service-task.md) · [Correlation & conversations](../operating/correlation.md) · [How events are processed](../concepts/event-processing.md)
- Design: [ADR-014 — Message handling](../../design/ADR-014-message-handling.md) · [ADR-016 — Message correlation](../../design/ADR-016-message-correlation.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/activities`
