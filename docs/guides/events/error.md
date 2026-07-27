---
title: Error events
description: Throw and catch a BPMN error; error boundary.
---

# Error events

A **BPMN error** is a modeled, named failure an activity raises to hand control
to a handler instead of faulting the whole instance. Your Go code **throws** it
as a typed `BpmnError` carrying a **code**; an **error boundary** attached to the
activity — armed with an `Error` object of a matching `errorCode` — **catches**
it and routes a token onto its exception flow. This is the structured way to
model "payment declined", "gateway down", or any expected-but-exceptional
outcome. This page is the developer reference — the trigger object, its
constructors, the boundary wiring, the `BpmnError` contract, and the runtime
match behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Event → Error (`ErrorEventDefinition`, §10.5.6) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/events` |
| Trigger type | `events.ErrorEventDefinition` (wraps a `bpmncommon.Error`) |
| Thrown value | `events.BpmnError` (a Go `error` with a `Code`) |
| Trigger tag | `flow.TriggerError` (`ErrorEventDefinition.Type()`) |
| Carried by | an **error boundary** (`events.BoundaryEvent`, always interrupting) |
| The work | match a raised `BpmnError.Code` to a boundary's `errorCode`, route to the exception flow |

Where it sits in the event family: [Events taxonomy](index.md).

## Constructors

An error trigger is built in two layers: the `Error` object (name + `errorCode`
+ optional payload structure), then the `ErrorEventDefinition` that wraps it.

```go
func bpmncommon.NewError(
    name, code string,
    str *data.ItemDefinition,
    baseOpts ...options.Option,
) (*bpmncommon.Error, error)

func events.NewErrorEventDefinition(
    cErr *bpmncommon.Error,
    baseOpts ...options.Option,
) (*events.ErrorEventDefinition, error)
```

| Parameter | Meaning |
|---|---|
| `name` | the error's diagram name (and default id source). |
| `code` | the `errorCode` the engine matches — the identity of the error. |
| `str` | optional `*data.ItemDefinition` describing the error payload structure; pass `nil` for a bare coded error. |
| `cErr` | the `Error` object to wrap; `NewErrorEventDefinition(nil, …)` is rejected. |

Both return an error, never panic, on an invalid combination.

## The throw side — `BpmnError`

Your activity **raises** an error by returning a `BpmnError`. Only a `BpmnError`
whose `Code` matches an error boundary routes to it; any other (untyped) error
faults the instance.

```go
func events.NewBpmnError(code string, cause error) (*events.BpmnError, error)
```

```go
type BpmnError struct {
    Err  error  // the optional underlying cause
    Code string // the errorCode matched against a boundary's errorRef
}
```

| Member | Role |
|---|---|
| `Code` | the `errorCode`; must equal a boundary's `Error.ErrorCode()` to route. An empty code is rejected at construction — a BPMN Error is identified by its code, and an uncoded error can match no boundary. |
| `Err` | the optional wrapped cause. |
| `Error() string` | renders the coded error (with the cause appended when set). |
| `Unwrap() error` | exposes `Err`, so `BpmnError` plays with `errors.Is` / `errors.As`. |

> An error boundary is **always interrupting** (BPMN §10.5.6). gobpm rejects
> `NewBoundaryEvent(..., cancelActivity=false)` for an error trigger at
> construction — unlike timer, message, signal, conditional, and escalation
> boundaries, which may be non-interrupting.

## Build it — throw and catch

Build the `Error` object, wrap it in an `ErrorEventDefinition`, and attach it to
the host activity with an **interrupting** boundary (`cancelActivity=true`). This
is exactly the wiring `examples/service-task-worker/` uses to catch a worker's
Business Error:

```go
bpErr, _ := bpmncommon.NewError("gateway-down", "PaymentGatewayDown", nil)
eed, _   := events.NewErrorEventDefinition(bpErr)
boundary, _ := events.NewBoundaryEvent("pay-bnd", authorize, eed, true) // interrupting
```

The guarded activity **raises** the matching error by returning a `BpmnError`
with the same code:

```go
op, _ := gooper.New("authorize-payment",
    func(ctx context.Context, _ service.DataReader,
        _ *data.ItemDefinition) (*data.ItemDefinition, error) {
        // ... attempt the authorization ...
        return events.NewBpmnError("PaymentGatewayDown", errGatewayUnreachable)
    })
```

Wire the boundary's outgoing flow to the handler — the exception path — and the
normal flow to the success end, so the two outcomes diverge:

```go
flow.Link(payment, endPaid)          // normal completion
flow.Link(boundary, failed)          // the boundary's exception flow
```

## Run it

`examples/service-task-worker/` throws a worker Business Error onto an error
boundary. The **boundary-interruption mechanism** itself is easiest to watch in
`examples/boundary-events/` — an interrupting *timer* boundary on a ~4s payment
task. An error boundary fires the same way; only the trigger differs (a raised
`BpmnError.Code` in place of an elapsed timer):

```bash
cd examples/boundary-events && go run .
```

The boundary fires, cancels the activity, and the token flows to the handler:

```
  → process-payment: charging the card (takes ~4s)...
  ✗ process-payment: interrupted before it finished
  → cancel-order: payment timed out, releasing the reservation

✓ boundary-events completed (Completed): the 2s timer boundary fired before the
4s payment finished — it interrupted the activity and routed to cancel-order
```

## Methods & runtime behavior

The engine drives the trigger and boundary — you rarely call these directly:

| Method | Role |
|---|---|
| `ErrorEventDefinition.Error() *bpmncommon.Error` | the wrapped `Error` object (name, code, payload). |
| `ErrorEventDefinition.Type() flow.EventTrigger` | reports `flow.TriggerError`. |
| `Error.ErrorCode() string` | the `errorCode` compared against a raised `BpmnError.Code`. |
| `BoundaryEvent.CancelActivity() bool` | whether the boundary interrupts (always true for error). |
| `BoundaryEvent.AttachedTo() flow.ActivityNode` | the guarded host activity. |

Behavior worth knowing:

- The boundary is **armed when a token arrives on the host** activity and stays
  armed for as long as the activity runs.
- On a raised `BpmnError`, the engine **compares its `Code`** to each error
  boundary's `errorCode`. A match cancels the guarded activity, discards its
  result at the interruption checkpoint, and routes a token onto the boundary's
  exception flow. **No match faults the instance.**
- A plain (untyped) `error` never routes — it always faults the instance. Use it
  for genuinely unexpected bugs, and a `BpmnError` for modeled outcomes.
- An error boundary is **always interrupting**; a non-interrupting error boundary
  is rejected at construction.

## See also

- Examples: `examples/service-task-worker/` (error boundary on a worker fault) · `examples/boundary-events/` (the interrupting-boundary mechanism)
- Related guides: [Boundary events](boundary.md) · [Escalation](escalation.md) · [Service Task](../tasks/service-task.md)
- Design: [ADR-018 — Boundary events and activity interruption](../../design/ADR-018-boundary-events-and-activity-interruption.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/events`
