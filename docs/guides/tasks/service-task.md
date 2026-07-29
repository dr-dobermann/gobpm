---
title: Service Task
description: Run your own Go code as a process step — in-process or dispatched to an external worker — with its constructor, options, and the Operation contract.
---

# Service Task

A Service Task runs automated work — your Go code, executed by the engine as a
step. It has two execution loci: **in-process** (the engine calls your
`Operation` synchronously) and **external worker** (the engine enqueues a job
and parks the task until a worker fetches, runs, and reports it). This page is
the developer reference — the type, its constructor, every option, the contract
you implement, and its runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Activity → Task → **Service Task** (§10.3.6) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/activities` |
| Type | `activities.ServiceTask` |
| Inherits | the `Activity` attributes and associations — I/O sets, boundary events, loop characteristics, compensation |
| Implements | `flow.Node`, `exec.NodeExecutor` (`Exec`), `exec.NodeDataConsumer`/`Producer` (`LoadData`/`UploadData`), `flow.ActivityNode` (`ActivityType`, `AddBoundaryEvent`) |
| The work | a `service.Operation` (your code), or a `WithWorker` topic |

Where it sits in the activity family: [Activities taxonomy](index.md).

## Constructor

```go
func NewServiceTask(
    name string,
    operation service.Operation,
    taskOpts ...options.Option,
) (*ServiceTask, error)
```

| Parameter | Meaning |
|---|---|
| `name` | the task's diagram name (and default id source). |
| `operation` | the work to run — a `service.Operation`. Build one from a plain Go func with `gooper.New`, or supply your own. |
| `taskOpts` | zero or more options (below). |

It returns an error — never panics — on an invalid combination (e.g. a worker
option on a non-message operation, or a nil policy/mapper).

## Options

Most Service Tasks need only a handful:

| Option | When you reach for it |
|---|---|
| `WithoutParams()` | the operation reads process data by name via its `DataReader` (no declared I/O). |
| `WithParameters(dir, params…)` | declare typed `data.Input` / `data.Output` parameters. |
| `WithTimeout(d)` | bound an in-process operation and make it context-cancellable. |
| `WithWorker(topic)` | dispatch the work to an external worker instead of running it in-process. |

The full set comes from two families — **activity options** (any activity) and
**service-task options** (`SrvTaskOption`, worker/timeout specific):

| Activity option | Effect |
|---|---|
| `WithParameters(dir data.Direction, params ...*data.Parameter)` | declare typed inputs/outputs. |
| `WithoutParams()` | declare no parameters. |
| `WithCompensation()` | mark the task a compensation handler (armed, off the normal flow). |
| `WithLoop(lc)` / `WithMultyInstance()` | repeat the activity — see [Standard Loop](../iteration/standard-loop.md), [Multi-Instance](../iteration/multi-instance.md). |
| `WithStartQuantity(n)` / `WithCompletionQuantity(n)` | BPMN token quantities (default 1). |

| Service-task option | Effect |
|---|---|
| `WithTimeout(d time.Duration)` | bound the in-process operation; `Exec` runs it in a sub-goroutine that honours cancellation. |
| `WithWorker(topic string)` | make the task an external-worker wait node (enqueue on `topic`, park until reported). Message-operation only. |
| `WithWorkerTrust(mode)` | `WorkerTrusted` (worker outcome authoritative — the default) vs `EngineAuthoritative`. |
| `WithRetryPolicy(p tasks.RetryPolicy)` | per-task technical-fault retry policy for the worker path (nil rejected). |
| `WithErrorMapper(m tasks.ErrorMapper)` | classify a worker's raw fault into Business Error / Business Status / technical (nil rejected). |
| `WithOutputMapping(rules ...tasks.OutputRule)` | assemble the worker's flat result into structured outputs by path. |

> Boundary events are attached with the method `AddBoundaryEvent`, not a
> constructor option — see [Boundary events](../events/boundary.md).

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/activities`.

## The Operation contract

The work is a `service.Operation`. The shortcut `gooper.New(name, fn)` wraps a
Go function as one; the function gets a read-only `service.DataReader` and
returns the item to commit (or `nil`):

```go
type Operation interface {
    foundation.Identifyer
    Name() string
    Type() string
    Errors() []string
    Clone() (Operation, error)
    Execute(ctx context.Context, r DataReader) (*data.ItemDefinition, error)
    BindInputOnly(ctx context.Context, r DataReader) (*data.ItemDefinition, error)
    // …BindOutputOnly for the worker path
}
```

You implement `Execute` (the work); `Clone` gives each instance its own copy;
`Errors` declares the fault classes the operation may raise.

## In-process execution

The operation runs synchronously inside the engine. `WithoutParams` lets it
reach data by name:

```go
op, _ := gooper.New("greet",
    func(ctx context.Context, r service.DataReader,
        _ *data.ItemDefinition) (*data.ItemDefinition, error) {
        user, _ := r.GetData("user_name")
        fmt.Printf("  ▶ hello, %v\n", user.Value().Get(ctx))
        return nil, nil
    })

task, _ := activities.NewServiceTask("work", op, activities.WithoutParams())
```

Running `examples/basic-process/`:

```
  ▶ hello, dr.Dobermann (instance started at 2026-07-26 …)
✓ basic-process completed (Completed): start → service task → end
```

## External-worker execution

`WithWorker(topic)` turns the task into a wait node; a worker registered on the
dispatcher for that topic does the work out-of-process, with retries and output
mapping configured on the task:

```go
task, _ := activities.NewServiceTask("reserve-stock",
    reserveOp,                        // a message operation
    activities.WithWorker("reserve"),
    activities.WithRetryPolicy(policy),
    activities.WithoutParams())
```

Running `examples/service-task-worker/` — a flaky worker retried in-process
under `WorkerTrusted`, then an output-mapped result:

```
  reserve attempt 1: inventory timeout — worker retries in-process…
  reserve attempt 3: reserved (reservationId=R-1001, zone=A-3)
  authorize: AUTHORIZED (Business Status)
  ✓ completed (Completed) → shipped [paymentStatus=AUTHORIZED, reservationId=R-1001, warehouseZone=A-3]
```

External workers have their own page: [External workers](../operating/external-workers.md).

## Methods & runtime behavior

The engine drives the task through these — you rarely call them directly:

| Method | Role |
|---|---|
| `Exec(ctx, re) ([]*flow.SequenceFlow, error)` | run the operation (in-process) or park on the job (worker); return the outgoing flows. |
| `LoadData` / `UploadData` | bind declared inputs before, commit outputs after. |
| `AddBoundaryEvent(be)` / `BoundaryEvents()` | attach / inspect boundary events. |
| `ActivityType()` / `Implementation()` | introspection. |
| `ForCompensation()` | whether the task is a compensation handler. |
| `Dehydratable(ctx, re) bool` | reports `false` — a worker job is active work in flight, not a passive wait, so it never releases the instance. |

Behavior worth knowing: an in-process operation runs on the track goroutine
unless `WithTimeout` moves it to a cancellable sub-goroutine; a worker task
**parks** (releases its goroutine) until the dispatcher reports the job; a task
with an error boundary interrupts on a matching fault.

Unlike a parked User Task, a worker task never **dehydrates** the instance: the
dispatcher's job lock is active work, not an engine-held wake source, so the
instance stays resident until the worker reports
([Persistence & recovery](../operating/persistence.md)).

## See also

- Examples: `examples/basic-process/` (in-process) · `examples/service-task-worker/` (worker)
- Related guides: [User Task](user-task.md) · [Script Task](script-task.md) · [Business Rule Task](business-rule-task.md) · [External workers](../operating/external-workers.md)
- Design: [ADR-021 — Service Task execution model](../../design/ADR-021-service-task-execution-model.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/activities`
