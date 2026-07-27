---
title: Terminate end event
description: End a whole instance or scope at once.
---

# Terminate end event

A plain end event consumes the one token that reaches it and lets the other
branches run on. A **terminate end event** carries a *terminate trigger*: the
instant one token arrives, the engine tears down the whole enclosing scope —
every other in-flight branch is cancelled and the instance settles in
`Terminated` instead of `Completed`. Reach for it when one branch decides there
is no point continuing: a fraud hit, a hard business rule, an unrecoverable
input. It is an ordinary `EndEvent` plus a `TerminateEventDefinition` trigger —
this page is the developer reference for building and running one.

## Taxonomy

| | |
|---|---|
| BPMN category | Event → End Event → **Terminate** trigger (§13.5.6 / `TerminateEventDefinition`) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/events` |
| Type | `events.EndEvent` carrying an `events.TerminateEventDefinition` |
| Trigger | `flow.TriggerTerminate` (`"Terminate"`) — returned by `TerminateEventDefinition.Type()` |
| Inherits | the end-event attributes (`BaseElement` id/documentation/extensions) |
| The work | collapse the enclosing scope — cancel sibling tracks, settle the instance `Terminated` |

Where it sits in the event family: [Events taxonomy](index.md).

## Constructor

There is no dedicated `NewTerminateEndEvent`. You build a normal end event and
attach a terminate trigger. Two calls:

```go
func NewTerminateEventDefinition(
    baseOpts ...options.Option,
) (*TerminateEventDefinition, error)

func NewEndEvent(
    name string,
    endEventOptions ...options.Option,
) (*EndEvent, error)
```

| Parameter | Meaning |
|---|---|
| `baseOpts` | zero or more base-element options for the definition (`foundation.WithID`, `foundation.WithDoc`). |
| `name` | the end event's diagram name (and default id source). |
| `endEventOptions` | end-event options — pass `WithTerminateTrigger(ted)` to make it a terminate. |

Both return an error, never panic, on an invalid argument or option combination.

## Options

The terminate is one option on an otherwise plain end event:

| Option | When you reach for it |
|---|---|
| `WithTerminateTrigger(ted)` | turn the end event into a terminate — the only thing that distinguishes it from a plain end event. |

```go
func WithTerminateTrigger(
    ted *TerminateEventDefinition,
) options.Option
```

`WithTerminateTrigger` adds the `TerminateEventDefinition` into the end event's
config. Drop it and you are left with a plain end event that only consumes its
own token. An end event accepts the other end-event trigger options too —
`WithErrorTrigger`, `WithEscalationTrigger`, `WithCancelTrigger`,
`WithCompensationTrigger`, `WithSignalTrigger`, `WithMessageTrigger` (see
[Start & End](start-and-end.md)); terminate is the one that collapses the scope.

> The trigger definition needs no arguments of its own —
> `NewTerminateEventDefinition()` with no base options is the usual call. Its
> behavior is entirely in *what the engine does* when the token arrives.

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/events`.

## Build it

Build the trigger, then attach it to an end event with `WithTerminateTrigger`.
The rest of the process is ordinary — a diverging parallel gateway splits into
two branches; the fraud branch ends at the terminate event, the payment branch
at a normal end event (from `examples/terminate-end-event/process.go`):

```go
termEd, err := events.NewTerminateEventDefinition()
// ...
terminate, err := events.NewEndEvent("terminate-order",
    events.WithTerminateTrigger(termEd))
// ...
paymentDone, err := events.NewEndEvent("payment-done") // a plain end event

split, _ := gateways.NewParallelGateway(gateways.WithDirection(gateways.Diverging))
flow.Link(start, split)
flow.Link(split, fraudCheck)
flow.Link(fraudCheck, terminate)   // this branch collapses the instance
flow.Link(split, payment)
flow.Link(payment, paymentDone)    // this branch is cancelled mid-charge
```

The payment operation is long-running and **honors its context** — that is what
lets the engine cancel it when the terminate fires (from `handlers.go`):

```go
select {
case <-time.After(3 * time.Second):
    fmt.Println("  ✓ process-payment: charged")
    return nil, nil
case <-ctx.Done():
    fmt.Println("  ✗ process-payment: interrupted before it finished")
    return nil, ctx.Err()
}
```

## Run it

```bash
cd examples/terminate-end-event && go run .
```

The payment branch starts charging; the fraud branch fires the terminate; the
payment context is cancelled before it can finish, and the instance settles in
`Terminated`:

```
  ⚠ fraud-check: fraudulent order detected — terminating the process
  → process-payment: charging the card (takes ~3s)...
InstanceState Terminating instance_id=…
  ✗ process-payment: interrupted before it finished
InstanceState Terminated instance_id=…

✓ terminate-end-event finished (Terminated): the fraud branch hit a Terminate
  End Event and ended the whole instance before the payment completed
```

> The two branches run concurrently, so `fraud-check` and `process-payment` may
> print in either order. What is deterministic is the outcome: the payment is
> interrupted and the instance ends `Terminated`.

## Methods & runtime behavior

The engine drives the end event; you rarely call these directly. The two that
matter for a terminate:

| Method | Role |
|---|---|
| `EndEvent.Exec(ctx, re) ([]*flow.SequenceFlow, error)` | consume the arriving token; a terminate trigger makes it collapse the scope instead of retiring one token. |
| `TerminateEventDefinition.Type() flow.EventTrigger` | reports `flow.TriggerTerminate` — how the engine recognises the trigger. |

Behavior worth knowing:

- **One token is enough.** The moment a token reaches the terminate end event,
  the engine flips the instance to `thresher.StateTerminating`, cancels the
  `context.Context` of every other active track, then settles the instance in
  `thresher.StateTerminated`. It does **not** wait for the other branches to
  finish.
- **Terminated is not Completed.** A normal instance that finishes all branches
  ends `thresher.StateCompleted`; a terminate collapses it to
  `thresher.StateTerminated`. Read the terminal state from the handle to tell
  them apart:

  ```go
  state, _ := h.WaitCompletion(ctx) // blocks to a terminal state
  // state == thresher.StateTerminated after a terminate end event
  ```

- **Cancellation is cooperative.** The engine cancels each track's context; a
  long-running operation must `select` on `ctx.Done()` to actually stop. An
  operation that ignores its context runs to completion — its side effects still
  land — even though the instance is already terminating.
- **Scope reach.** A terminate ends the scope it lives in. At the top level of a
  process that is the whole instance; inside an embedded sub-process it ends that
  sub-process's scope and the outer process continues from the sub-process's
  outgoing flow. See [Embedded Sub-Process](../subprocesses/embedded.md).

## See also

- Examples: `examples/terminate-end-event/`
- Related guides: [Start & End](start-and-end.md) · [Parallel (AND)](../gateways/parallel.md) · [Exclusive (XOR)](../gateways/exclusive.md) · [Instance lifecycle](../operating/instance-lifecycle.md)
- Design: [ADR-001 — Execution model](../../design/ADR-001-execution-model.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/events`
