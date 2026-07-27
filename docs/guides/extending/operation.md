---
title: Custom Operation
description: Implement service.Operation directly (beyond gooper).
---

# Custom Operation

The work a [Service Task](../tasks/service-task.md) runs is a
`service.Operation` — the executable contract the engine calls (in-process) or
whose input contract it binds (when the task is dispatched to a worker). For
most work you never implement it: `gooper.New` wraps a plain Go func as an
Operation, and that is the recommended path. Reach for a direct implementation
when you need something `gooper` doesn't give you — a canonical BPMN
message operation (`inMessage`→`outMessage`), an operation backed by a stateful
service object, custom `Clone` semantics, or a bespoke `Type()`/`Errors()`
contract.

This page shows the seam interface, how the engine takes your Operation, a
minimal real implementation, and when to reach past `gooper`.

## The seam interface

An Operation is polymorphic (ADR-011 §2.6): the same interface backs the
gobpm-native Go operation (`gooper`) and the canonical BPMN message operation.
You implement all of it:

```go
type Operation interface {
    foundation.Identifyer // ID() string

    // Name returns the operation's name.
    Name() string

    // Type returns the operation's implementation mechanism.
    Type() string

    // Errors returns the error classes the operation may return.
    Errors() []string

    // Clone returns a per-instance copy of the operation, or an error when
    // a carried element cannot be rebuilt (never panics).
    Clone() (Operation, error)

    // Execute runs the operation against the per-execution reader r and returns
    // the item to commit as the activity's output (nil if none).
    Execute(ctx context.Context, r DataReader) (*data.ItemDefinition, error)

    // BindInputOnly binds the operation's input message from r without
    // executing it, returning the bound input item (nil if the operation has
    // no input message). A worker-dispatched ServiceTask uses it to build the
    // job payload.
    BindInputOnly(ctx context.Context, r DataReader) (*data.ItemDefinition, error)
}
```

| Member | You implement it to… |
|---|---|
| `ID()` | return a stable id (embed `foundation.BaseElement` and it's free). |
| `Name()` | name the operation for diagnostics and diagram introspection. |
| `Type()` | report your implementation mechanism — a self-chosen tag (`gooper` uses `"##GoOper"`). |
| `Errors()` | declare the fault classes `Execute` may raise, so the task can classify them. |
| `Clone()` | give each instance its own copy — return an error, never panic, if a carried element can't be rebuilt. |
| `Execute(ctx, r)` | do the work (in-process locus); read process data through `r`, return the item to commit or `nil`. |
| `BindInputOnly(ctx, r)` | bind only the input message (worker locus); return `nil` when there is no input message. |

> `Execute` and `BindInputOnly` receive a `service.DataReader` — the narrow,
> read-only data surface: `GetData(name)`, `GetDataByID(id)`, `GetSources()`,
> `List(path)`. A runtime variable is read by its explicit `"RUNTIME/<var>"`
> path, a process property by its plain name. No writes, no lifecycle, no
> events. See [Reading & writing by path](../data/structural.md).

## How the engine takes your Operation

There is no registry to call — an Operation is passed straight into the task
constructor, which validates it:

```go
func NewServiceTask(
    name string,
    operation service.Operation,
    taskOpts ...options.Option,
) (*ServiceTask, error)
```

At startup each instance `Clone`s the task's operation so per-instance state
never leaks between runs. Then, per execution:

1. **In-process** (no `WithWorker`) — the engine calls `Execute(ctx, r)` on the
   track goroutine (or a cancellable sub-goroutine under `WithTimeout`) and
   commits the returned item as the task's output.
2. **External worker** (`WithWorker(topic)`) — the engine calls
   `BindInputOnly(ctx, r)` to build the job payload, enqueues it, and parks the
   task; the worker is the executor, so the operation contributes only its input
   contract.

See [Service Task](../tasks/service-task.md) for the full option set (worker,
timeout, retry, output mapping) and [External workers](../operating/external-workers.md)
for the worker locus.

## A minimal implementation

Most direct implementations don't need to write the interface by hand — one of
the two built-in constructors already implements it for you:

| You want… | Reach for | Result |
|---|---|---|
| a Go func as the work | `gooper.New(name, fn, opts…)` | native Go operation (`Type() == "##GoOper"`) |
| a BPMN `inMessage`→`outMessage` operation | `service.NewOperation(name, inMsg, outMsg, implementor, opts…)` | canonical message operation |

The message path lets you supply your own executor as a `service.Implementor`
and hand it to `NewOperation` — that is the smallest way to run bespoke logic
inside a fully-fledged message operation without writing the seven-method
interface yourself:

```go
type Implementor interface {
    Type() string          // your mechanism tag
    ErrorClasses() []string // faults you may raise (on top of the built-in classes)
    Execute(ctx context.Context, in *data.ItemDefinition) (*data.ItemDefinition, error)
}

op, err := service.NewOperation("reserve", inMsg, outMsg, myImplementor)
if err != nil {
    return err
}
task, _ := activities.NewServiceTask("reserve-stock", op, activities.WithoutParams())
```

`NewOperation` returns an error (never panics) on an invalid combination;
`service.MustOperation` is the panicking sibling for known-good static wiring.
The message operation already carries `ObjectNotFound`, `EmptyNotAllowed` and
`OperationFailed` error classes — your `ErrorClasses()` adds to them.

If you genuinely need a full hand-rolled `Operation` (custom `Clone`, a `Type()`
neither built-in offers, an operation wrapping a long-lived service object),
implement the seven members above, embed `foundation.BaseElement` for `ID()`,
and make `Clone` return a deep-enough copy that instances don't share mutable
state.

## Reference implementations

Two built-ins are the models to copy:

| Implementation | Package | Shape |
|---|---|---|
| **Go operation** (`gooper`) | `pkg/model/service/gooper` | wraps an `OpFunctor`; `Execute` calls your func with the reader and bound input, `BindInputOnly` binds the (optional) in-message. The everyday path. |
| **Message operation** | `pkg/model/service` (`NewOperation`) | canonical BPMN `inMessage`→`outMessage`; delegates the actual work to your `Implementor`. Use `service.BindInput` to bind a message from scope. |

`gooper`'s `OpFunctor` is the exact signature the wrapper adapts:

```go
type OpFunctor func(
    ctx context.Context,
    r service.DataReader,
    in *data.ItemDefinition,
) (*data.ItemDefinition, error)
```

Both the `service-task-worker` example (below) and `basic-process` build their
work through `gooper.New` — start there, and only drop to a direct `Operation`
when the table above says you must.

## Run it

`examples/service-task-worker/` builds its operations directly and runs them
across the in-process and worker loci. A real run (banner trimmed):

```
order-normal (amount 50):
  reserve attempt 1: inventory timeout — worker retries in-process…
  reserve attempt 3: reserved (reservationId=R-1001, zone=A-3)
  authorize: AUTHORIZED (Business Status)
  ✓ completed (Completed) → shipped [paymentStatus=AUTHORIZED, reservationId=R-1001, warehouseZone=A-3]
```

## See also

- Related guides: [Service Task](../tasks/service-task.md) · [External workers](../operating/external-workers.md) · [Reading & writing by path](../data/structural.md)
- Examples: `examples/service-task-worker/` · `examples/basic-process/`
- Design: [ADR-011 — process data flow](../../design/ADR-011-process-data-flow.md) · [ADR-021 — Service Task execution model](../../design/ADR-021-service-task-execution-model.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/service` · `go doc github.com/dr-dobermann/gobpm/pkg/model/service/gooper`
