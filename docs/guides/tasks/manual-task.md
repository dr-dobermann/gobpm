---
title: Manual Task
description: A no-op placeholder step for work done outside the engine.
---

# Manual Task

A Manual Task marks work performed **without any IT system** — a human filling
a paper form, a technician swapping a part — so the diagram stays honest about a
step even though the engine does nothing for it (BPMN §13.1). gobpm treats it as
a **no-op pass-through**: on activation the token flows straight to the outgoing
sequence flow(s) with no data distribution and no wait. Reach for it when you
want the step *visible* on the model but *unexecuted* by the engine.

## Taxonomy

| | |
|---|---|
| BPMN category | Activity → Task → **Manual Task** (§13.1, a non-operational element) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/activities` |
| Type | `activities.ManualTask` |
| Inherits | the `Activity` attributes and associations — I/O sets, boundary events, loop characteristics, compensation, roles |
| Implements | `flow.Node`, `exec.NodeExecutor` (`Exec`), `exec.NodeDataConsumer`/`Producer` (`LoadData`/`UploadData`), `flow.ActivityNode` (`ActivityType`, `AddBoundaryEvent`) |
| The work | none — the engine never runs it (no-op) |

Where it sits in the activity family: [Activities taxonomy](index.md).

## Constructor

```go
func NewManualTask(
    name string,
    opts ...options.Option,
) (*ManualTask, error)
```

| Parameter | Meaning |
|---|---|
| `name` | the task's diagram name (and default id source). |
| `opts` | zero or more foundation / activity options (below). |

Unlike a Service Task it takes **no operation** — there is nothing to run. It
returns an error, never panics, on an invalid option.

## Options

A Manual Task is a placeholder, so most declarations need **no options at all**:

```go
task, _ := activities.NewManualTask("inspect-parcel")
```

When you do configure it, only the **activity** and **role** option families
apply — the task carries the generic Activity attributes but none of the
execution-specific ones (there is no operation, worker, timeout, or human
assignee to configure):

| Activity option | Effect |
|---|---|
| `WithParameters(d data.Direction, params ...*data.Parameter)` | declare typed inputs/outputs (documentation only — the task never reads or writes them). |
| `WithoutParams()` | declare no parameters. |
| `WithCompensation()` | mark the task a compensation handler (armed, off the normal flow). |
| `WithLoop(lc)` / `WithMultyInstance()` | repeat the activity — see [Standard Loop](../iteration/standard-loop.md), [Multi-Instance](../iteration/multi-instance.md). |
| `WithStartQuantity(n)` / `WithCompletionQuantity(n)` | BPMN token quantities (default 1). |

| Role option | Effect |
|---|---|
| `WithRoles(ress ...*hi.ResourceRole)` | attach resource roles (who is nominally responsible for the off-system work). |

> Boundary events are attached with the method `AddBoundaryEvent`, not a
> constructor option — see [Boundary events](../events/boundary.md).

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/activities`.

## Usage

There is no interface to implement and nothing to wire — build the task and drop
it on the flow between two nodes:

```go
inspect, _ := activities.NewManualTask("inspect-parcel")
// … place `inspect` in the process between its predecessor and successor;
// at runtime the token passes straight through with no pause.
```

Because the engine performs no work, there is no captured output to show — the
step simply advances. If you need the engine to *do* something, pick an
executable task instead: [Service Task](service-task.md) (your Go code),
[Script Task](script-task.md), or [User Task](user-task.md) (a human step the
engine actually tracks and waits on).

## Methods & runtime behavior

The engine drives the task through these — you rarely call them directly:

| Method | Role |
|---|---|
| `Exec(ctx, re) ([]*flow.SequenceFlow, error)` | no-op pass-through: binds nothing, advances straight to the outgoing flows. |
| `LoadData` / `UploadData` | inherited data phases; with no operation reading them, they carry no effect on the run. |
| `AddBoundaryEvent(be)` / `BoundaryEvents()` | attach / inspect boundary events. |
| `ActivityType()` / `ForCompensation()` | introspection. |
| `Clone()` | per-instance copy — a fresh activity shell over the shared config. |

Behavior worth knowing: `Exec` never distributes work and never parks — the
token reaches the outgoing sequence flow(s) immediately on activation. A Manual
Task therefore never blocks an instance and never appears in a task list; it is
purely a modelling marker. This matches the BPMN Process Execution Conformance
allowance that the engine MAY treat a Manual Task as a no-op (see design note).

## See also

- Related guides: [User Task](user-task.md) (a human step the engine tracks) · [Service Task](service-task.md) · [Script Task](script-task.md) · [Activities taxonomy](index.md)
- Design: [ADR-020 — Human interaction execution model](../../design/ADR-020-human-interaction-execution-model.md) (§2.10 — the no-op pass-through decision)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/activities`
