---
title: Event sub-processes
description: In-scope handlers, interrupting or non-interrupting.
---

# Event sub-processes

An **Event Sub-Process** is a handler that lives *inside* a scope and fires when
an event catches — the boundary-event pattern lifted from a single activity's
window to the whole enclosing scope's window. Reach for it when work anywhere in
a scope needs a shared reaction: a scope-wide timeout, a cancellation message, a
caught error. It is a `SubProcess` you mark `triggeredByEvent` and place inside
another scope; no token flows into it — it is **armed** while that scope is open
and fires when its single triggered **Start Event** catches. This page is the
developer reference — the type, its marker, the interrupting flag on its start,
the contract it validates, and its runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Activity → Sub-Process → **Event Sub-Process** (§13.5.4) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/activities` |
| Type | `activities.SubProcess` (built with `WithTriggeredByEvent()`) |
| Inherits | the `SubProcess` container — inner graph via `Add` + `flow.Link`, the same-container rule |
| Implements | `flow.Node`, `exec.NodeExecutor` (`Exec`), `flow.ActivityNode` (`ActivityType`, `AddBoundaryEvent`) — plus `IsEventSubProcess()` / `IsTransaction()` mode introspection |
| The work | its own inner flow, seeded from **one triggered Start Event** instead of a None start |

Where it sits in the family: [Composition taxonomy](../subprocesses/index.md) ·
[Embedded Sub-Process](../subprocesses/embedded.md). For the per-activity
version of the same catch-and-react pattern, see
[Boundary events](boundary.md).

## Constructor

An Event Sub-Process is a plain `SubProcess`; the marker `WithTriggeredByEvent()`
is what distinguishes it:

```go
func NewSubProcess(name string, opts ...options.Option) (*SubProcess, error)
```

| Parameter | Meaning |
|---|---|
| `name` | the handler's diagram name (and default id source). |
| `opts` | zero or more options — for a handler, at least `WithTriggeredByEvent()`. |

It returns an error — never panics — on an invalid combination. The mode
constraint is checked at `Validate()` (run through `Process.Validate`): a
`triggeredByEvent` sub-process must have **exactly one** triggered Start Event
(Message / Timer / Signal / Error / Conditional), not the None-start or the
flow-less entry an embedded Sub-Process uses.

## Options

Most handlers need only the marker plus a triggered start:

| Option | Where | When you reach for it |
|---|---|---|
| `activities.WithTriggeredByEvent()` | on the `SubProcess` | mark it a scope-armed handler — the one required option. |
| `events.WithTimerTrigger(...)` (or `WithMessageTrigger`, `WithSignalTrigger`, `WithErrorTrigger`, `WithConditionalTrigger`) | on its Start Event | give the handler its trigger. |
| `events.WithNonInterrupting()` | on its Start Event | make the handler **fork** on each fire instead of cancelling the scope. |

The interrupting-vs-non-interrupting mode is a property of the handler's **Start
Event**, not of the sub-process. The full sub-process family:

| `SubProcessOption` | Effect |
|---|---|
| `WithTriggeredByEvent()` | mark the sub-process an Event Sub-Process — armed, entered only by its triggered start (BPMN §13.5.4). |
| `WithTransaction()` | the *other* sub-process mode — see [Transaction Sub-Process](../subprocesses/transaction.md). Not a handler mode. |

And the interrupting flag, set on the handler's Start Event (both from
`pkg/model/events`):

| Start-event option | Effect |
|---|---|
| `WithInterrupting()` | the default (§13.5.4) — explicit documentation; the handler cancels its scope on fire. |
| `WithNonInterrupting()` | flip to non-interrupting — the handler runs concurrently, re-fires, and never cancels the scope. Rejected on an Error trigger (Error starts are always interrupting, §10.5.6). |

> **Note:** An Event Sub-Process is armed, not linked. No sequence flow enters
> it — placing a `WithTriggeredByEvent()` sub-process in a scope with one
> triggered start is all the wiring it needs. Boundary events, by contrast, are
> attached with `AddBoundaryEvent`.

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/activities` and
`go doc github.com/dr-dobermann/gobpm/pkg/model/events`.

## The handler contract

There is no interface a handler implements beyond the ordinary `SubProcess`
methods. The *marker* changes the container's shape, and `Validate()` enforces
it — so the contract is: exactly one triggered Start Event, an interrupting one
unless its start opts into `WithNonInterrupting()`. Build the inner graph like
any sub-process (`Add` every element, `flow.Link` the pairs); the difference is
that the seed is a triggered start, not a None start.

## Build it

The handler is an ordinary `SubProcess` with two things: the
`WithTriggeredByEvent()` marker, and a Start Event carrying a **trigger** (here
a timer):

```go
timeout, _ := activities.NewSubProcess("payment-timeout",
    activities.WithTriggeredByEvent())

tStart, _ := events.NewStartEvent("timeout-fired",
    events.WithTimerTrigger(timeoutTimer()))
release, _ := step("releaseHold")
tEnd, _ := events.NewEndEvent("timeout-end")
// add tStart, release, tEnd to `timeout`; link tStart → release → tEnd
```

Then add the handler to the scope it guards — the same `Add` you use for any
element. Here it goes inside the `await-payment` sub-process, alongside that
scope's own nodes (`timeout` is the last element in the slice):

```go
await, _ := activities.NewSubProcess("await-payment")
wire(await,
    []flow.Element{pStart, pay, charge, pEnd, timeout}, // timeout is the handler
    [2]flow.Element{pStart, pay},
    [2]flow.Element{pay, charge},
    [2]flow.Element{charge, pEnd})
```

The `awaitPay` node is a `ReceiveTask` waiting on a message that never arrives,
so the timer wins the race:

```go
func awaitPayment(name string) (*activities.ReceiveTask, error) {
    return activities.NewReceiveTask(name,
        bpmncommon.MustMessage("payment",
            data.MustItemDefinition(values.NewVariable(1))))
}
```

For a **non-interrupting** handler, mark the *start event* instead — the
sub-process stays the same:

```go
tStart, _ := events.NewStartEvent("timeout-fired",
    events.WithTimerTrigger(timeoutTimer()),
    events.WithNonInterrupting()) // fork on each fire; never cancel the scope
```

## Run it

```bash
cd examples/event-subprocess && go run .
```

The observer narrates the scope and handler lifecycle (operator log lines
elided):

```
  checkout
  ▶ scope await-payment: Opened
  ⚡ handler payment-timeout: Armed
  ⚡ handler payment-timeout: Fired
  ▶ scope payment-timeout: Opened
  ⚡ handler payment-timeout: Disarmed
  releaseHold
  ▶ scope payment-timeout: Completed
  ▶ scope await-payment: Completed
  notify
  ✓ completed (Completed)
```

The wait never charged: the timer interrupted it, `releaseHold` ran, and the
parent resumed on its normal flow to `notify`.

## Watching it: the handler fact

You watch arming and firing through an `observability.Observer`. A handler
transition arrives as a `KindBoundary` fact — the same kind a plain boundary
event uses — distinguished by an `AttrScopePath` detail: a handler carries a
scope path, a plain boundary event does not.

```go
func (p *scopePrinter) OnFact(f observability.Fact) {
    switch f.Kind {
    case observability.KindScope:
        fmt.Printf("  ▶ scope %s: %s\n", f.NodeName, f.Phase)
    case observability.KindBoundary:
        if _, isHandler := f.Details[observability.AttrScopePath]; isHandler {
            fmt.Printf("  ⚡ handler %s: %s\n", f.NodeName, f.Phase)
        }
    }
}
```

The handler `Phase` walks `Armed` → `Fired` → `Disarmed`; the child scope it
runs in reports `KindScope` with `Opened` → `Completed`. See
[Observability](../concepts/observability.md) for the full fact vocabulary.

## Methods & runtime behavior

The engine drives the handler; you rarely call these directly:

| Method | Role |
|---|---|
| `IsEventSubProcess() bool` | whether this sub-process is a `triggeredByEvent` handler — the runtime uses it to arm rather than seed-on-entry. |
| `Exec(ctx, re) ([]*flow.SequenceFlow, error)` | run the handler's inner flow once armed and fired. |
| `Add(e) / Remove(e)` | build the inner graph (its triggered start + body). |
| `Validate()` | enforce the one-triggered-start rule. |
| `IsTransaction() bool` | the other sub-process mode (false for a handler). |

Behavior worth knowing:

- **Armed, not entered.** The handler is armed when its enclosing scope opens
  and disarmed when it fires (interrupting) or when the scope closes. No token
  ever flows into it.
- **Interrupting (default, §13.5.4).** On fire it **cancels** the scope's
  in-flight work while the scope's data plane stays open — so the handler runs
  in the parent's data context — runs its own flow in a fresh child scope seeded
  from the triggered start, then **absorbs** the event by reaching its End
  without re-throwing, so the parent resumes on its **normal** outgoing flow,
  not a boundary path.
- **Non-interrupting.** Each fire **forks** a concurrent handler instance in its
  own child scope **without** cancelling the scope, and it can re-fire unlimited
  times. The scope completes once its own work *and* every handler instance
  drain.
- **Error trigger.** An Error-triggered handler catches on the scope chain and
  is interrupting-only (BPMN §10.5.6) — `WithNonInterrupting()` on an Error
  start is rejected.

## See also

- Examples: `examples/event-subprocess/`
- Related guides: [Boundary events](boundary.md) · [Embedded Sub-Process](../subprocesses/embedded.md) · [Timer](timer.md) · [Observability](../concepts/observability.md)
- Design: [ADR-023 — Sub-Process & Call Activity](../../design/ADR-023-sub-process-and-call-activity.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/activities`
