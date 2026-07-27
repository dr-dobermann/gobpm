---
title: Event-based gateway
description: A deferred choice; the first event wins.
---

# Event-based gateway

Where an [exclusive gateway](exclusive.md) picks an arm by evaluating *data*, an
**event-based** gateway defers the choice to the outside world. It arms several
intermediate catch events at once, parks, and routes the token down whichever
**fires first** — dropping the rest. Reach for it when the next step depends on
which event arrives: an approval message versus a timeout, a cancellation versus
a confirmation. This page is the developer reference — the type, its
constructor, every option, and its runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Gateway → **Event-Based Gateway** (§13.4.4, WCP-16) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/gateways` |
| Type | `gateways.EventBasedGateway` |
| Embeds | `gateways.Gateway` — direction, flow attributes |
| Implements | `flow.Node`, `exec.NodeExecutor` (`Exec`); engine-driven `ProcessEvent` / `ArmFor` / `Definitions` / `EventClass` |
| The work | subscribe to every downstream catch arm; route the first-fired event into its arm, drop the others |

Where it sits in the gateway family: [Gateways taxonomy](index.md).

## Constructor

```go
func NewEventBasedGateway(opts ...options.Option) (*EventBasedGateway, error)
```

| Parameter | Meaning |
|---|---|
| `opts` | zero or more options (below). Mid-flow needs only `WithDirection(Diverging)`; the instantiating form adds `WithInstantiate` (and optionally `WithEventGatewayType` / `WithCorrelationKey`). |

It returns an error — never panics — on an invalid combination (e.g. a nil
correlation key). Arm and start **well-formedness** (BPMN §10.6.6 / §10.5.6 —
every arm an intermediate catch event, `ParallelEvents` only with
`WithInstantiate`) is checked later, at process registration, by `Validate`.

## Options

Most event gateways are a mid-flow deferred choice and need only one option:

| Option | When you reach for it |
|---|---|
| `WithDirection(Diverging)` | the ordinary mid-flow gate: one token in, several event arms out. |
| `WithInstantiate()` | make the gate a process **start** — no incoming flow, no start event; the first arm's event creates the instance. |
| `WithEventGatewayType(ParallelEvents)` | on a start gate, complete only once *every* arm has fired (default `ExclusiveEvents`). |
| `WithCorrelationKey(key)` | on a parallel start gate, route later arm messages to the instance the first message created. |

The options come from two typed families — **gateway options** (any gateway)
and **event-based options** (`EventBasedOption`, start/correlation specific):

| Gateway option | Effect |
|---|---|
| `WithDirection(dir GDirection)` | set the direction — `Diverging` for a mid-flow split gate. |

| Event-based option | Effect |
|---|---|
| `WithInstantiate()` | mark the gate a process-start instantiator (no incoming flow); an event at one arm starts an instance (BPMN §10.5.6 / §13.2). |
| `WithEventGatewayType(t EventGatewayType)` | start policy: `ExclusiveEvents` (default — first event wins / each event starts its own instance) or `ParallelEvents` (start-only — first event creates one instance, completes once all arms fired). |
| `WithCorrelationKey(key *bpmncommon.CorrelationKey)` | one key whose per-arm retrieval expression lets the starter derive the same conversation key from whichever arm fires first and route the remaining arms to that instance (BPMN §8.4.2). nil is rejected. |

> `WithEventGatewayType(ParallelEvents)` is start-only — it requires
> `WithInstantiate` and is rejected at registration otherwise. A mid-flow gate
> is always Exclusive.

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/gateways`.

## The arms

The gate implements no interface *you* provide — you supply its arms. Each arm
is an ordinary `events.IntermediateCatchEvent` (a message, timer, signal, or
conditional catch) wired directly downstream of the gate by a sequence flow.
Nothing special marks a catch as a gateway arm; the flows from the gate do:

```go
flow.Link(start, gate)
flow.Link(gate, approvalArm)   // arm 1 — a message catch
flow.Link(gate, timeoutArm)    // arm 2 — a timer catch
flow.Link(approvalArm, approved)
flow.Link(timeoutArm, timedOut)
```

> Every arm must be an intermediate *catch* event directly downstream of the
> gate. A service task or a second gateway on an arm is not a valid target for
> the deferred choice — `Validate` rejects it at registration.

## Build it

Construct the gate diverging, build each arm as a catch event, then wire the
flows. From `examples/event-based-gateway/`:

```go
gate, _ := gateways.NewEventBasedGateway(
    gateways.WithDirection(gateways.Diverging))

approvalArm, _ := messageCatch("approval", approvalMessage) // message catch
timeoutArm, _ := timerCatch("timeout", 10*time.Second)      // timer catch
```

A message arm is a plain message catch; a timer arm fires after its duration
from *now*:

```go
def, _ := events.NewMessageEventDefinition(
    bpmncommon.MustMessage(msgName, data.MustItemDefinition(
        values.NewVariable(""), foundation.WithID(id+"_in"))), nil)
ice, _ := events.NewIntermediateCatchEvent(id, def)
```

## Run it

```bash
cd examples/event-based-gateway && go run .
```

The demo races an approval message against a 10-second timeout; the driver
publishes the approval after the gate has parked, so that arm wins:

```
deferred choice: waiting for an approval message OR a 10s timeout...
  ✓ approval arrived first → order approved
✓ event-based-gateway completed (Completed): the gate fired the arm whose event arrived first; the other was dropped
```

The driver starts the instance, waits for the gate to park on both arms, then
publishes:

```go
h, _ := engine.StartLatest(proc.ID())
time.Sleep(300 * time.Millisecond) // let the gate park on both arms
broker.Publish(ctx, messaging.Envelope{Name: approvalMessage, Payload: "OK"})
state, _ := h.WaitCompletion(ctx)
```

The timer arm is the self-terminating fallback: the run completes even if no
message ever arrives — bound its duration to your SLA.

## Instantiating form

`WithInstantiate()` turns the gate into a process start — no start event, no
incoming flow. With `ParallelEvents` and a correlation key it becomes a
message-born join: the first correlated message creates the instance, the
second re-arms keyed to it, and the instance completes once both have fired.
From `examples/event-based-parallel-start/`:

```go
gate, _ := gateways.NewEventBasedGateway(
    gateways.WithInstantiate(),
    gateways.WithEventGatewayType(gateways.ParallelEvents),
    gateways.WithCorrelationKey(key))
```

```
publishing 'order placed' (creates the instance)...
  ✓ order placed   → recorded
publishing 'payment received' (routes to the same instance)...
  ✓ payment received → recorded
✓ order-fulfillment completed (Completed): one instance, born by the first of two correlated messages
```

The driver never calls `StartLatest` here — it discovers the event-born
instance through the engine's instance-observation API and waits for it.

## Methods & runtime behavior

The engine drives the gate through these — you rarely call them directly:

| Method | Role |
|---|---|
| `Exec(ctx, re) ([]*flow.SequenceFlow, error)` | on token arrival, subscribe to every arm and park; on the first fire, return that arm's outgoing flow. |
| `ProcessEvent(ctx, eDef)` | engine callback delivering a fired arm's event to the gate. |
| `ArmFor(eDef) (flow.Node, bool)` | resolve which arm a fired event definition belongs to. |
| `Definitions() []flow.EventDefinition` | the event definitions the gate subscribes to (its arms). |
| `Instantiate()` / `ParallelStart()` | whether the gate is a start instantiator, and whether its start policy is parallel. |
| `CorrelationKey()` | the declared correlation key (instantiating parallel form), or nil. |

Behavior worth knowing:

- **Subscribe-all, then park.** On arrival the gate registers a waiter for
  every downstream catch and blocks. It does *not* pick an arm eagerly — the
  choice belongs to the events. No token ever sits on an arm.
- **First fire wins.** When one arm's event is delivered, its token proceeds
  and the gate cancels the other subscriptions, so the losing arms never run.
- **Exclusive vs parallel is a start policy.** A mid-flow gate is always
  Exclusive. `ParallelEvents` is meaningful only on an instantiating gate,
  where the instance completes once *every* arm has fired.

## See also

- Examples: `examples/event-based-gateway/` (mid-flow) · `examples/event-based-parallel-start/` (instantiating parallel start)
- Related guides: [Exclusive (XOR)](exclusive.md) · [Message](../events/message.md) · [Timer](../events/timer.md) · [Correlation & conversations](../operating/correlation.md)
- Design: [ADR-005 — Gateways and joins](../../design/ADR-005-gateways-and-joins.md) · [ADR-015 — Event-triggered instantiation](../../design/ADR-015-event-triggered-instantiation.md) · [ADR-016 — Message correlation](../../design/ADR-016-message-correlation.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/gateways`
