---
title: Custom task distributor
description: Deliver user tasks to your own front end.
---

# Custom task distributor

When a process reaches a UserTask, the engine **parks** it — the track releases
its goroutine and waits for a human to act. To reach that human you implement a
`interactor.TaskDistributor`: the embedder-provided boundary the engine calls to
**announce** a newly parked task and to **withdraw** one that is no longer
completable. It is your inbox, your web front end, your ticket queue — wired into
the engine like the message broker or the clock. This page shows the seam, the
registration call, a minimal implementation, and how the engine drives it.

> The distributor does **not** execute the task. It only surfaces availability.
> The human acts back through the engine's own `Take`/`Complete` entry points,
> which the engine authorizes — instance data never reaches the distributor
> before an authorized `Take` (ADR-020 §2.8).

## The TaskDistributor contract

```go
type TaskDistributor interface {
    // Distribute announces a parked UserTask as available for human work.
    Distribute(ctx context.Context, task TaskInfo) error

    // Withdraw retracts a task that is no longer completable — it was completed,
    // or its activity was canceled (e.g. an interrupting boundary event fired).
    Withdraw(ctx context.Context, taskID string) error
}
```

Two methods, both a pure notification sink:

| Method | The engine calls it when… | You do… |
|---|---|---|
| `Distribute(ctx, task)` | a UserTask parks and becomes available | route/persist the announcement so a human can find it |
| `Withdraw(ctx, taskID)` | the task is completed or canceled | remove it from every inbox — it can no longer be taken |

The announcement is a `TaskInfo` — identity plus the roles that may claim it, and
**deliberately no task data**:

```go
type TaskInfo struct {
    TaskRef
    Roles []*hi.ResourceRole
}

type TaskRef struct {
    TaskID     string
    InstanceID string
    NodeID     string
    ProcessID  string
}
```

`TaskID` is the handle you carry back to the engine to act on the task; the rest
identifies where it lives. `Roles` (candidate users/groups) is for inbox routing
and filtering — who *may* claim it — not authorization, which the engine enforces
at `Take`.

## Registering it

Pass your distributor to the engine at construction:

```go
func WithTaskDistributor(d interactor.TaskDistributor) Option
```

```go
th, err := thresher.New("approval-engine",
    thresher.WithTaskDistributor(myDistributor))
```

The default is a no-op distributor (`interactor.NopDistributor()`) — tasks still
park and are completable by id, but nothing is announced. Reach for a real one
the moment a human needs to *discover* a task rather than being handed its id.

## Acting on the task: the engine entry points

`Distribute`/`Withdraw` are outbound. To let a human actually work a task, your
front end calls back into the engine — these are the entry points a distributor
(or its UI) needs:

| Entry point | Role |
|---|---|
| `Take(ctx, taskID, actor) (TaskView, error)` | authorize the actor, then return the authorized snapshot — renderers to build the UI plus the task's `Data`. |
| `Complete(ctx, taskID, actor, outputs) error` | authorize, validate the outputs, and resume the parked track. |

`Take` returns a `TaskView` (`TaskRef` + `Renderers` + `Data`) — it carries data
only *after* the acting `Actor` passes authorization, the counterpart to the
data-free `TaskInfo`.

## Reference implementation: the console driver

`pkg/interactor/console` ships a batteries-included distributor you can read as
the canonical example. Its `Driver` is a `TaskDistributor` that, on announcement,
drives the whole cycle on a background goroutine (so the instance loop is never
blocked): it `Take`s the task as a single `Actor`, renders the form to collect
outputs, and `Complete`s it.

```go
func New(actor hi.Actor, w io.Writer) *Driver
func (d *Driver) Bind(e Engine)
func (d *Driver) Distribute(ctx context.Context, task interactor.TaskInfo) error
func (d *Driver) Withdraw(_ context.Context, taskID string) error
```

Note the two-step wiring, forced by a cycle: the driver needs the engine (to call
`Take`/`Complete`) and the engine needs the driver (via `WithTaskDistributor`).
Build the driver first, pass it to the engine, then `Bind` the engine back:

```go
driver := console.New(operator{}, os.Stdout)

th, err := thresher.New("approval-engine",
    thresher.WithTaskDistributor(driver))
if err != nil {
    return err
}

driver.Bind(th)
```

The `Bind` target is a narrow `Engine` interface — exactly the slice of the
Thresher a driver needs — so your own distributor can depend on the same minimal
surface:

```go
type Engine interface {
    Take(
        ctx context.Context, taskID string, actor hi.Actor,
    ) (interactor.TaskView, error)
    Complete(
        ctx context.Context, taskID string, actor hi.Actor, outputs []data.Data,
    ) error
}
```

## How the engine uses it

Running `examples/usertask/` — a `start → approve (UserTask) → end` process with
the console driver as the distributor — shows the full cycle. The task parks, the
driver is announced, Takes, renders, Completes, and the engine withdraws it:

```
task available: id=6393123168117620361 node=8270086057682901648
... TaskState Announced  node_name=approve task_id=6393123168117620361
... TaskState Taken      node_name=approve task_id=6393123168117620361
Approve? type a decision
... TaskState Completed  node_name=approve task_id=6393123168117620361
task withdrawn: id=6393123168117620361
... TaskState Withdrawn  task_id=6393123168117620361
task completed: id=6393123168117620361
... InstanceState Completed
process finished: Completed
```

The `Announced → Taken → Completed → Withdrawn` sequence is the contract in
motion: `Distribute` fires the announcement, your front end drives `Take` and
`Complete`, and the engine calls `Withdraw` once the task leaves the completable
set. For a real deployment your `Distribute` writes to a queue or inbox and a
separate request handler calls `Take`/`Complete` when the human acts — the
console driver simply collapses both halves into one goroutine.

## See also

- Examples: `examples/usertask/`
- Related guides: [User Task](../tasks/user-task.md) · [Human tasks](../operating/human-tasks.md)
- Design: [ADR-020 — Human-interaction execution model](../../design/ADR-020-human-interaction-execution-model.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/interactor`
