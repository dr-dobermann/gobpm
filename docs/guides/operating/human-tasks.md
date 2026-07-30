---
title: Human tasks
description: The task distributor — list, assign, and complete user tasks across the engine boundary.
---

# Human tasks

A [User Task](../tasks/user-task.md) is work a person does. When the engine
reaches one it does not run code — it **parks** the track and announces the
task to a **task distributor** you supply, then
waits. A human, acting through the engine, later **takes** the task (reads its
form), does the work, and **completes** it with outputs; only then does the
track resume. This page is the runtime picture: what crosses the engine
boundary, the public contracts (`interactor`), and how a distributor drives a
task to completion.

The internal *why* — the park-on-the-event-channel mechanics and the assignment
triad — lives in
[ADR-020](../../design/ADR-020-human-interaction-execution-model.md); why a
parked task lets the whole instance release its goroutines is in
[Persistence & recovery](persistence.md).

## The boundary at a glance

The engine owns execution; the distributor owns the human. They meet at two
call directions:

| Direction | Call | Carries |
|---|---|---|
| Engine → distributor | `Distribute(ctx, TaskInfo)` | identity + roles, **no data** |
| Engine → distributor | `Withdraw(ctx, taskID)` | the id of a task no longer completable |
| Human → engine | `Take(ctx, taskID, actor)` | returns a `TaskView` (renderers + data) |
| Human → engine | `Complete(ctx, taskID, actor, outputs)` | the submitted outputs |

The distributor never drives execution. It is *told* a task is available and it
*asks* the engine to act — the engine authorizes every read and write against
the acting actor. Data never leaves the engine until an authorized `Take`.

## The TaskDistributor contract

You plug a distributor in with `thresher.WithTaskDistributor`; the default is a
no-op (tasks still park and stay completable by id). The interface is small:

```go
type TaskDistributor interface {
    // Distribute announces a parked UserTask as available for human work.
    Distribute(ctx context.Context, task TaskInfo) error

    // Withdraw retracts a task that is no longer completable — it was
    // completed, or its activity was canceled (e.g. an interrupting boundary
    // event fired).
    Withdraw(ctx context.Context, taskID string) error
}
```

`Distribute` fires when a UserTask parks — route it into an inbox, a queue, a
UI. `Withdraw` fires when the task can no longer be completed: it was completed,
or its activity was canceled (an interrupting boundary event, a terminated
instance). A distributor that keeps a live inbox removes the row on `Withdraw`.

> The distributor is injected like any other engine boundary
> (`MessageBroker`, `Clock`). Build a custom one against this interface — see
> [Custom task distributor](../extending/task-distributor.md). `NopDistributor()`
> returns the default.

## Ownership: who is working on it

A parked task is offered to everyone eligible, but only one person may hold it.
Completion is strict — **only the holder may complete** — so an unclaimed task
is completable by nobody.

| Call | Who may | Notes |
|---|---|---|
| `Claim(ctx, taskID, actor)` | any eligible actor, if nobody else holds it | re-claiming your own task is a no-op, so claim-before-complete is retry-safe |
| `Unclaim(ctx, taskID, actor)` | the holder only | returns it to the pool |
| `Reassign(ctx, taskID, userID)` | **anyone — the engine does not check** | for the operator cases below; the *nominee* is still checked against the task's triad |

`Reassign` is deliberately unguarded because its callers are not participants:
a manager assigning a responsible person, an administrator rescuing a task from
someone on sick leave, an offboarding flow moving a departing employee's queue.
None of them would pass the task's own candidate check, so gating on it would
forbid every legitimate use. **Two operational consequences:**

- **You decide who may reassign, and you must log it.** The engine records
  `Reassigned` with the old and new holder, but cannot name the *caller* — it
  never authorized one. If who-moved-this-task matters to your audit, log it on
  your side.
- **Bulk moves are yours too.** Reassigning everything one departing employee
  holds spans many instances; the engine's surface is per task. Your inbox
  already knows which tasks exist and who holds them — loop over it.

These operations never touch the process: they do not advance, resume or cancel
anything, and they do **not** wake a dehydrated instance. Only completion does.
So a claim during a three-day wait costs nothing.

Ownership does **not** survive an engine restart, and does not protect a task
from cancellation — an interrupting boundary event still tears down a held task.

## Who performed a task

Completion records the performer for later nodes to route on, in the engine's
read-only `RUNTIME` area:

```
RUNTIME/COMPLETED_BY   →   map: node name → the user who completed it
```

It is written by the engine and cannot be forged or overwritten by the process,
and it survives dehydration. Note it names whoever actually **finished** the
task — after a reassignment, the new holder, not the original assignee.

## What crosses the boundary

Three value types travel between engine and distributor. They share an
identity header (`TaskRef`) but differ deliberately in what data they carry —
nothing before authorization, everything after.

| Type | Produced by | Fields | Why |
|---|---|---|---|
| `TaskRef` | embedded | `TaskID`, `InstanceID`, `NodeID`, `ProcessID` | identifies a parked task across the boundary. |
| `TaskInfo` | `Distribute` | `TaskRef` + `Roles []*hi.ResourceRole` | the pre-authorization announcement: identity + the roles that may claim it, and **no task data** (variables must not reach the distributor before an authorized `Take`). |
| `TaskView` | `Take` | `TaskRef` + `Renderers []hi.Renderer` + `Data []data.Data` | the post-authorization snapshot: the renderers to build the UI and the self-describing data (inputs plus properties such as a `FORM_ID`). |

`TaskInfo` carries roles for inbox routing/filtering; it withholds data by
design. `TaskView` is returned only after the acting actor passes authorization,
so — unlike `TaskInfo` — it carries the task's data.

## The acting human

Both `Take` and `Complete` take an `Actor` — the authenticated party acting on
the task, distinct from the BPMN `Performer` role *declaration*:

```go
type Actor interface {
    UserID() string   // matched against assignee and candidateUsers
    Groups() []string // matched against candidateGroups
}
```

The engine authorizes an actor against the task's assignment triad (assignee /
candidate users / candidate groups). An authorization failure from `Take` or
`Complete` is **non-terminal** — the task stays parked, and another actor (or
the same one with corrected identity) can try again.

## The engine entry points

The human acts through the engine, not the distributor. Both entry points live
on the `Thresher` and route to the owning instance:

| Method | Behavior |
|---|---|
| `Take(ctx, taskID, actor) (TaskView, error)` | authorize `actor`, return the `TaskView`. On auth failure: error, no data, task stays parked. |
| `Complete(ctx, taskID, actor, outputs) error` | authorize, then validate `outputs`; only if **both** pass, bind the outputs and resume the parked track. Auth or validation failure is non-terminal — task stays parked. |

`Complete` is the only path that resumes the track. Its outputs ride back into
the instance loop as a synthetic completion event (`interactor.TaskCompletion`),
delivered on the same parked event channel a message would use — the track was
never returned, just held.

## Driving a task: the console distributor

`pkg/interactor/console` is a batteries-included reference distributor: on each
announcement it Takes the task on a background goroutine, renders its form to
collect outputs, and Completes it. Build it, pass it to the engine, then `Bind`
the engine back so it can call `Take`/`Complete`:

```go
driver := console.New(operator{}, os.Stdout)

th, err := thresher.New("approval-engine",
    thresher.WithTaskDistributor(driver))
// …
driver.Bind(th)
```

The `Bind` two-step exists because the distributor is constructed *before* the
engine (it is a constructor argument), yet it needs the engine to act — the
`console.Engine` interface is exactly the slice it calls: `Take` and `Complete`.

Running `examples/usertask/` — a UserTask claimable by candidate user
`operator`, collecting a `decision` output, auto-completed from a scripted
console form:

```
task available: id=8744061684987302244 node=3498287155606046952
… TaskState Announced  node_name=approve
… TaskState Taken      node_name=approve
Approve? type a decision
… TaskState Completed  node_name=approve
task withdrawn: id=8744061684987302244
… TaskState Withdrawn
task completed: id=8744061684987302244
… InstanceState Completed
process finished: Completed
```

The trace shows the full arc: **Announced** (`Distribute`) → **Taken**
(`Take`) → **Completed** (`Complete`) → **Withdrawn** (`Withdraw`, because the
task is now done) → instance completes.

## Behavior worth knowing

- **Parking, not blocking.** A parked UserTask releases nothing of the engine's
  scheduling — the track goroutine is released while it waits, so a slow human
  never starves other work. With a repository configured the whole instance
  **dehydrates**: the task keeps living in the distributor's inbox, which is
  exactly why the instance need not, so a task pending for weeks costs zero
  goroutines ([Persistence & recovery](persistence.md)).
- **No data before auth.** `Distribute` gets identity and roles only; the first
  time task data materializes across the boundary is a successful `Take`.
- **Failures are non-terminal.** A rejected `Take`/`Complete` leaves the task
  parked and completable; only a real `Complete` or a cancellation moves it.
- **Withdraw covers both endings.** A completed task and a canceled activity
  both fire `Withdraw` — a live inbox drops the row either way.
- **The default is silent-but-completable.** With no distributor wired
  (`NopDistributor`), tasks still park and can be driven by id through
  `Take`/`Complete` — you just get no announcement.

## See also

- Example: `examples/usertask/` (console-driven approval)
- Related guides: [User Task](../tasks/user-task.md) · [Custom task distributor](../extending/task-distributor.md) · [Custom authorization](../extending/authorization.md) · [External workers](external-workers.md)
- Design: [ADR-020 — Human interaction execution model](../../design/ADR-020-human-interaction-execution-model.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/interactor`
