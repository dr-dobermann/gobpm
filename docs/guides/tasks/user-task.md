---
title: User Task
description: A human step: assign, list, complete.
---

# User Task

A User Task is a step a **person** performs. When execution reaches it the
engine parks the task — it releases the track goroutine and announces the task
to a `TaskDistributor` — and waits: an eligible actor claims it, fills in its
form, and completes it. Only then does the token flow on. This page is the
developer reference: the type, its constructor, every option, the `Renderer`
contract, and its wait-node runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Activity → Task → **User Task** (§10.3.3) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/activities` |
| Type | `activities.UserTask` |
| Inherits | the `Activity` attributes and associations — I/O sets, boundary events, loop characteristics, compensation |
| Implements | `flow.Node`, `exec.NodeExecutor` (`Exec`), `exec.NodeDataConsumer`/`Producer` (`LoadData`/`UploadData`), `flow.ActivityNode` (`ActivityType`, `AddBoundaryEvent`) |
| The work | a human, driven by a `TaskDistributor`, filling in a `hinteraction.Renderer` form |

Where it sits in the activity family: [Activities taxonomy](index.md).

## Constructor

```go
func NewUserTask(
    name string,
    userTaskOpts ...options.Option,
) (*UserTask, error)
```

| Parameter | Meaning |
|---|---|
| `name` | the task's diagram name (and default id source). |
| `userTaskOpts` | zero or more options (below) — the assignment triad, renderer(s), and declared outputs. |

It returns an error — never panics — on an invalid option (e.g. a nil renderer,
or an assignment slot given both static and expression forms).

## Options

Most User Tasks need only these:

| Option | When you reach for it |
|---|---|
| `WithCandidateUsers(ids…)` | a pool of eligible users; the first to claim it owns it. |
| `WithAssignee(id)` | a single owning user — only that user may act. |
| `WithRenderer(r)` | attach the form the human fills in. |
| `WithOutput(name, type, required)` | declare a value the form collects. |
| `WithoutParams()` | the task declares no process I/O parameters. |

The full set comes from three families — **user-task options** (the assignment
triad, renderers, outputs), the **activity options** any activity accepts, and
the **foundation/data options**.

**User-task options** decide *who* may act and *what* the form collects. Each
assignment slot has a static form and an expression form (evaluated per
instance from a `data.FormalExpression`); the two forms are mutually exclusive:

| User-task option | Effect |
|---|---|
| `WithAssignee(userID string)` | single owning user; only a matching `UserID` is authorized (the restrictive gate). |
| `WithAssigneeExpr(expr data.FormalExpression)` | the assignee, computed per instance. |
| `WithCandidateUsers(userIDs ...string)` | pool of eligible users; a matching user is authorized. |
| `WithCandidateUsersExpr(expr data.FormalExpression)` | the candidate users, computed per instance. |
| `WithCandidateGroups(groupIDs ...string)` | any member of an intersecting group is authorized. |
| `WithCandidateGroupsExpr(expr data.FormalExpression)` | the candidate groups, computed per instance. |
| `WithRenderer(r hinteraction.Renderer)` | attach a form; call it more than once for multiple renderings (deduplicated by identity). |
| `WithOutput(name, pType string, required bool)` | declare a value the form collects; `required: true` rejects completion without it. |

**Activity options** (shared by every activity):

| Activity option | Effect |
|---|---|
| `WithParameters(dir data.Direction, params ...*data.Parameter)` | declare typed inputs/outputs. |
| `WithoutParams()` | declare no parameters. |
| `WithCompensation()` | mark the task a compensation handler (armed, off the normal flow). |
| `WithLoop(lc)` / `WithMultyInstance()` | repeat the activity — see [Standard Loop](../iteration/standard-loop.md), [Multi-Instance](../iteration/multi-instance.md). |
| `WithStartQuantity(n)` / `WithCompletionQuantity(n)` | BPMN token quantities (default 1). |

**Foundation & data options:** `WithID`, `WithDoc`, `WithProperties`.

> Boundary events are attached with the method `AddBoundaryEvent`, not a
> constructor option — see [Boundary events](../events/boundary.md).

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/activities`.

## The Renderer contract

The form is a `hinteraction.Renderer`. On completion the `TaskDistributor`
calls `Render` to collect the outputs the human supplies:

```go
type Renderer interface {
    foundation.Identifyer
    foundation.Documentator

    // Implementation returns the type of implementation it provides.
    Implementation() string

    // Render shows a data form to the user and gathers the user's inputs,
    // returned as a data.Data slice. ds provides values needed for rendering.
    Render(ds data.Source) ([]data.Data, error)
}
```

You implement `Render` (show the form, return the collected `data.Data`) and
`Implementation` (the rendering type). The package ships a console renderer,
`consinp`, so you need not write one to get started — build one with
`consinp.NewRenderer` and declare its fields with `WithStringInput` /
`WithIntInput` / `WithMessager`, and (for a scripted run) `WithSource`.

## Build it

Build the task with its assignment, form, and declared output, then wire it
like any other node. Here `approve` is claimable by the candidate user
`operator` and collects a required `decision` string (from
[`examples/usertask/`](../../../examples/usertask/)):

```go
form, _ := consinp.NewRenderer(
    consinp.WithStringInput("decision", "Approve? type a decision"),
    consinp.WithSource(bytes.NewBufferString("approved\n")),
)

ut, _ := activities.NewUserTask("approve",
    activities.WithCandidateUsers("operator"),
    activities.WithRenderer(form),
    activities.WithOutput("decision", "string", true),
    activities.WithoutParams(),
)
```

`WithSource` feeds the console renderer a scripted answer so the example runs
end-to-end with no interactive typing.

## Run it

A User Task needs a bound `TaskDistributor` — the component that surfaces
parked tasks to humans. The example uses the built-in console driver, acting
*as* an `operator` whose `UserID()` matches the task's candidate user:

```go
driver := console.New(operator{}, os.Stdout)

th, _ := thresher.New("approval-engine",
    thresher.WithTaskDistributor(driver))

driver.Bind(th)
```

`console.New(actor, w)` builds the driver acting as a specific human;
`WithTaskDistributor` registers it so the engine can announce parked tasks; and
`driver.Bind(th)` links the driver back to the engine so it can call `Take` and
`Complete` — the two halves must know each other. Running
`examples/usertask/`:

```
task available: id=6595855523137331530 node=3424710742457493363
TaskState Announced node_name=approve task_id=6595855523137331530
TaskState Taken     node_name=approve task_id=6595855523137331530
Approve? type a decision
TaskState Completed node_name=approve task_id=6595855523137331530
task withdrawn: id=6595855523137331530
task completed:  id=6595855523137331530
process finished: Completed
```

The engine announces the task (`Announced`), the driver **takes** it
(`Taken`), renders the form, and **completes** it (`Completed`); the collected
`decision` becomes task output and the token flows to `end`.

> Without a bound `TaskDistributor` a User Task still parks and is completable
> by id, but nothing surfaces it — the default distributor is a no-op. The
> human step never advances until something drives it.

The task distributor as an operating concern has its own page:
[Human tasks](../operating/human-tasks.md).

## Methods & runtime behavior

The engine drives the task through these — you rarely call them directly:

| Method | Role |
|---|---|
| `Exec(ctx, re) ([]*flow.SequenceFlow, error)` | reached **once**, after acceptance: bind the completed outputs into the frame and advance onto the outgoing flow(s). It never blocks. |
| `Authorize(ctx, actor, src, eng)` | verdict on whether `actor` may act, per the assignment triad (assignee restricts; else candidate user/group; no triad = open). |
| `ProcessEvent(ctx, eDef)` | store the synthetic completion event's outputs (already authorized + validated by the loop) for `Exec` to bind. |
| `ValidateOutputs(outputs)` | reject a completion missing a `required: true` output. |
| `Assignments()` / `Renderers()` / `Outputs()` | inspect the declared triad, forms, and expected outputs. |
| `AddBoundaryEvent(be)` / `BoundaryEvents()` | attach / inspect boundary events. |
| `Dehydratable(ctx, re) bool` | reports `true` — a parked human task lets the whole instance release its goroutines (the task keeps living in the distributor's inbox). |

Behavior worth knowing: a User Task is a **wait node**. It parks on the same
event channel as catch events — the track goroutine is released, not held —
and resumes only on an authorized, validated `Complete`. `Authorize` gates
`Take` and `Complete` against the actor identity the distributor supplies:
an `assignee` is the restrictive gate (only that user); otherwise a matching
`candidateUser` or an intersecting `candidateGroup` authorizes; a failed or
empty expression denies. A non-nil error from `Authorize` is a non-terminal
denial — the task stays parked, waiting for the right actor.

## See also

- Examples: [`examples/usertask/`](../../../examples/usertask/) (console-driven approval)
- Related guides: [Service Task](service-task.md) · [Human tasks](../operating/human-tasks.md) · [Process, instance, track, token](../concepts/execution-model.md) · [Boundary events](../events/boundary.md)
- Design: [ADR-020 — Human interaction execution model](../../design/ADR-020-human-interaction-execution-model.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/activities`
