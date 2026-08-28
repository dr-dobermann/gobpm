---
title: Transaction Sub-Process
description: ACID-like abort via Cancel.
---

# Transaction Sub-Process

A **Transaction Sub-Process** is an embedded Sub-Process with one extra
behavior: reaching a **Cancel End Event** inside it triggers an ACID-like
**abort** — the engine compensates the steps that already completed, terminates
anything still running, and leaves through a dedicated **Cancel boundary**
instead of the normal exit. Reach for it when a group of steps must either all
stick or roll back together: a booking, a payment, an order. This page is the
developer reference — the type, how to flag it, the events that make the abort
work, and its runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Activity → Sub-Process → **Transaction** (§10.7) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/activities` |
| Type | `activities.SubProcess` (with `WithTransaction(...)`) |
| Inherits | everything an embedded Sub-Process is — a nested scope, its own inner graph, boundary events, data walk-up |
| Implements | `flow.Node`, `exec.NodeExecutor` (`Exec`), `flow.ActivityNode` (`ActivityType`, `AddBoundaryEvent`); `IsTransaction() bool` reports the flag |
| The work | the inner graph, ending at a **Cancel End Event** whose abort is caught by an outer **Cancel boundary** |

Where it sits in the composition family: [Composition taxonomy](index.md). The
Transaction is not a distinct type — it is a `SubProcess` marked at
construction, so [Embedded Sub-Process](embedded.md) is its base in full.

## Constructor

Same constructor as any Sub-Process — the Transaction is a construction option,
not a separate type:

```go
func NewSubProcess(
    name string,
    opts ...options.Option,
) (*SubProcess, error)
```

| Parameter | Meaning |
|---|---|
| `name` | the sub-process's diagram name (and default id source). |
| `opts` | zero or more options — pass `WithTransaction(...)` to make it a Transaction. |

It returns an error — never panics — on an invalid option combination. The
shape rules (a legal inner graph, a Cancel End only inside a Transaction) are
enforced by `Validate` at **registration**, not at construction: a container is
built element by element and checked once complete.

## Options

The one option that defines this page, and the two it takes:

| Option | When you reach for it |
|---|---|
| `WithTransaction(opts ...TransactionOption)` | make the Sub-Process a Transaction — permit its Cancel End + Cancel boundary, name the scope a cancel aborts, and carry the two BPMN attributes below. With no options: a compensate transaction stating no protocol. |
| `WithTransactionMethod(m TransactionMethod)` | the BPMN `method` — which coordinator aborts the transaction. `TransactionCompensate` (`"compensate"`) is built in and the default; the model reads a document's `##Compensate` as the same thing. Any other identifier — `"store"`, `"##Image"`, a URI — is **carried** and refused by `RegisterProcess` as *no transaction coordinator is registered for this method* until a host can register one. A blank method is refused. |
| `WithTransactionProtocol(p string)` | the BPMN `protocol` — carried verbatim for loading and round-trip, handed to the coordinator, never read by the engine. A blank protocol is refused. |

`WithTransaction` is a `SubProcessOption` — the Sub-Process-specific family,
applied to the container itself. It is **mutually exclusive** with
`WithTriggeredByEvent()` (a handler is not a transaction). The two
`TransactionOption`s exist only inside `WithTransaction`, so a protocol on a
plain Sub-Process cannot be written. The full family:

| Sub-process option | Effect |
|---|---|
| `WithTransaction(opts...)` | ACID-like abort on a Cancel End (this page). |
| `WithTriggeredByEvent()` | make it an [Event Sub-Process](../events/event-subprocess.md) instead — mutually exclusive with `WithTransaction()`. |

Because a Transaction *is* a Sub-Process, the activity options apply too —
`WithCompensation()`, `WithLoop`, `WithStartQuantity`, and the rest; see
[Embedded Sub-Process](embedded.md#options) for that family.

> Boundary events — including the interrupting Cancel boundary — are attached
> with `NewBoundaryEvent`/`AddBoundaryEvent`, not a constructor option. See
> [Boundary events](../events/boundary.md).

## The abort, wired from three events

The Transaction marker only *permits* the abort; the abort itself is spelled out
with three ordinary events. All are grounded in `pkg/model/events`.

| Event | Constructor | Role |
|---|---|---|
| Cancel End | `NewEndEvent(name, WithCancelTrigger(ced))` | inside the scope — reaching it fires the abort. |
| Cancel boundary | `NewBoundaryEvent(name, tx, ced, true)` | outside the scope — the interrupting exit the abort leaves through. |
| Compensation boundary | `NewCompensationBoundaryEvent(name, host, ced, handler)` | links a completed step to its undo handler, making the step compensable. |

The Cancel trigger is a `CancelEventDefinition`:

```go
func NewCancelEventDefinition(baseOpts ...options.Option) (*CancelEventDefinition, error)
```

A step's undo handler is an ordinary task marked `WithCompensation()` — it lives
**outside** the normal flow and runs only when the abort sweeps the ledger. The
Compensation boundary that links host to handler validates that the handler is
so marked:

```go
func NewCompensationBoundaryEvent(
    name string,
    host flow.ActivityNode,
    def *CompensationEventDefinition,
    handler flow.ActivityNode,
    baseOpts ...options.Option,
) (*BoundaryEvent, error)
```

## Build it

Flag the Sub-Process, build its steps in a line ending at a Cancel End Event:

```go
tx, _ := activities.NewSubProcess("booking", activities.WithTransaction())

sStart, _ := events.NewStartEvent("s-start")
reserve, _ := stepTask("reserve-seat", "  ✓ seat reserved")
charge, _ := stepTask("charge-card", "  ✓ card charged")

cancEd, _ := events.NewCancelEventDefinition()
cancelBooking, _ := events.NewEndEvent("cancel-booking",
    events.WithCancelTrigger(cancEd))
```

Each undo handler is a task marked `WithCompensation()`, and a Compensation
boundary links its host step to it:

```go
release, _ := undoTask("release-seat", "  ↩ seat released") // WithCompensation()

ced, _ := events.NewCompensationEventDefinition(nil, true)
bndReserve, _ := events.NewCompensationBoundaryEvent(
    "comp-reserve", reserve, ced, release)
```

Outside the Transaction, an **interrupting** Cancel boundary catches the abort
and routes to the recovery path:

```go
cbEd, _ := events.NewCancelEventDefinition()
cancelBnd, _ := events.NewBoundaryEvent("cancel-bnd", tx, cbEd, true) // interrupting
// start → tx → end ; cancelBnd → notify-customer → cx-end
```

## Run it

```bash
cd examples/transaction-sub-process && go run .
```

The two steps run, the Cancel End aborts, the undo handlers fire in reverse
completion order, and control leaves through the Cancel boundary:

```
  ✓ seat reserved
  ✓ card charged
  ↩ card refunded
  ↩ seat released
Scope Canceled node_name=booking scope_path=/transaction-sub-process/sp-…
  ✉ customer notified: booking canceled
InstanceState Completed

✓ transaction-sub-process completed (Completed): the Cancel End compensated the
  booking in reverse order and control left through the Cancel boundary
```

Note `refund-card` (undo for `charge-card`) runs **before** `release-seat`
(undo for `reserve-seat`) — reverse of the order the steps completed in — and
the `Scope Canceled` fact prints *after* both undo handlers, evidence that
compensation runs before the scope teardown.

## Methods & runtime behavior

The engine drives the Transaction through the `SubProcess` methods (you rarely
call them directly); the one that distinguishes it:

| Method | Role |
|---|---|
| `Transaction() *TransactionCharacteristics` | the characteristics — `Method()` and `Protocol()` — or `nil` on a plain or Event Sub-Process. The runtime binds a Transaction scope to its coordinator by the method when the scope opens. |
| `IsTransaction() bool` | whether characteristics are present — the runtime uses it to resolve a Cancel abort to this scope; the model uses it to gate Cancel End/boundary placement. |
| `Exec(ctx, re) ([]*flow.SequenceFlow, error)` | open the child scope, run the inner graph, and either drain normally or abort on a Cancel End. |
| `ActivityType()` / `AddBoundaryEvent(be)` / `BoundaryEvents()` | activity introspection and boundary wiring (shared with every Sub-Process). |

Behavior worth knowing — reaching the Cancel End Event aborts the Transaction in
a **fixed, load-bearing order**:

1. **compensate** the completed activities — scope-wide, in **reverse
   completion order**, waiting for the handlers;
2. **terminate** any activities still running in the scope;
3. **leave** through the interrupting Cancel boundary onto the recovery path.

The order matters: compensation runs **before** the scope teardown, because the
teardown discards the very completion ledger the compensation sweeps. Further:

- A **Cancel End Event** performs no other end-event behavior — it wins the race
  like Terminate, and is **legal only inside a Transaction**. A Cancel End
  outside a Transaction is rejected at `RegisterProcess`
  (`ValidateCancelEndPlacement`); the Cancel trigger has meaning only as a
  Transaction abort.
- The **Cancel boundary** is a model-declared exit, always interrupting
  (`cancelActivity` true). It is resolved by the abort directly, never routed
  through the event bus.
- Compensation handlers must be marked `WithCompensation()`; unmarked tasks are
  ordinary flow nodes and are never swept by the abort.
- A Transaction that **completes normally** (an ordinary End Event) commits its
  ledger — no compensation runs. Only the Cancel path rolls back. Compensation
  and the Cancel boundary are independent: a Transaction may declare handlers
  without an outer Cancel boundary, or a Cancel boundary with nothing to undo.
- The abort runs through the **coordinator the scope was bound to** when it
  opened — today always the engine's own compensate sequence. `RegisterProcess`
  refuses a process whose Transaction names any other method, so a running
  scope is never bound to a coordinator the engine lacks. The abort's
  `Compensation`/`Thrown` fact carries `transaction_method` for the observer.

## Restarts

A Transaction **abort captured mid-compensation resumes after a
restart**: the sweep continues (the running undo re-runs — at-least-
once over its snapshot), the residual tracks are terminated once it
drains, and control leaves through the Cancel boundary exactly as a
resident abort would. A restored Transaction scope is bound to its
coordinator on restore exactly as a fresh one is on open.


## See also

- Examples: `examples/transaction-sub-process/`
- Related guides: [Embedded Sub-Process](embedded.md) (the base) · [Call Activity](call-activity.md) · [Compensation](../events/compensation.md) · [Boundary events](../events/boundary.md) · [Terminate](../events/terminate.md) (the other "wins immediately" end trigger) · [BPMN import coverage](../extending/bpmn-import-coverage.md) (how `<transaction method>`/`protocol` import)
- Design: [ADR-028 — Transaction Sub-Process: ACID-like abort by Cancel](../../design/ADR-028-transaction-sub-process.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/activities`
