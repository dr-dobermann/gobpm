---
title: Data Objects
description: Scope-resident named containers for per-instance data.
---

# Data Objects

A **DataObject** is a diagram-visible, named data container that lives in an
instance's scope. Where a `Property` is hidden engine state you seed at build
time, a DataObject is a *variable* a node writes at run time — a task's output
flows into it through a data association, and from then on it is an ordinary
scope-resident value reachable **by name**: from another node, an expression, or
the instance handle. It is per-instance — each instance clones its own copy, so
concurrent instances never share DataObject state.

This page is the data-model reference: the type, its constructor, the
association methods you call to wire it, and its runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Data → **Data Object** (§10.4.1) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/data_objects` (imported as `dataobjects`) |
| Type | `dataobjects.DataObject` |
| Embeds | `flow.BaseElement`, `data.ItemAwareElement` |
| Element type | `flow.DataObjectElement` (`"DataObject"`) — via `EType()`, so it can be `proc.Add`-ed to a Process/SubProcess |
| The work | holds one item value; fed by a `DataAssociation` from a producing node |

A DataObject is item-aware (it carries an `ItemDefinition` and a `DataState`),
which places it in the same family as `Property` and the task I/O parameters —
see [Item definitions & item-aware elements](item-definitions.md).

## Constructor

```go
func New(
    name string,
    idef *data.ItemDefinition,
    state *data.SrcState,
    baseOpts ...options.Option,
) (*DataObject, error)
```

| Parameter | Meaning |
|---|---|
| `name` | the object's diagram name and its by-name lookup key. |
| `idef` | the item it holds — an `*data.ItemDefinition` carrying the value and the id that ties an association to it. Build one with `data.MustItemDefinition(values.NewVariable(zero), foundation.WithID(id))`. |
| `state` | an optional `*data.SrcState` (BPMN Data State); pass `nil` for none. |
| `baseOpts` | zero or more `options.Option` (e.g. `foundation.WithID`). |

It returns an error — never panics — on an invalid item definition or option
combination.

> **Note:** `data.CreateDefaultStates()` must run once before building any
> item-aware element — it registers the standard data states (`Ready`,
> `Unavailable`, …). Skip it and construction fails.

## Wiring: the association methods

A DataObject does not read process data on its own — a producing node's output
is copied into it along a **data association**. The DataObject *is* the
association's target; `AssociateSource` builds that link:

| Method | Role |
|---|---|
| `AssociateSource(n flow.AssociationSource, sourceIDs []string, transformation data.FormalExpression) error` | make node `n`'s outputs (named by `sourceIDs`) the source; the DataObject is the target. The common case. |
| `AssociateTarget(n flow.AssociationTarget, transformation data.FormalExpression) error` | the reverse — feed the DataObject's value into node `n`'s input. |
| `Update(ctx context.Context) error` | recompute the object's state. |

```go
func (do *DataObject) AssociateSource(
    n flow.AssociationSource,
    sourceIDs []string,
    transformation data.FormalExpression,
) error
```

| Parameter | Meaning |
|---|---|
| `n` | the producing node — anything implementing `flow.AssociationSource` (e.g. a `ServiceTask`). |
| `sourceIDs` | the ids of `n`'s output parameters whose values flow into the object. The id is the wiring: it must match the DataObject's item-definition id. |
| `transformation` | a `data.FormalExpression` applied as the value flows in; `nil` for a straight copy. |

## Build it

Three pieces line up by a shared id (`resID`): the task **output parameter**,
the **DataObject's** item definition, and the **association**. From
[`examples/process-data/`](../../../examples/process-data/):

Declare the task output the operation fills —

```go
outParam := data.MustParameter(name+" result",
    data.MustItemAwareElement(
        data.MustItemDefinition(
            values.NewVariable(""),
            foundation.WithID(resID)),
        data.UnavailableDataState))

st, err := activities.NewServiceTask(name, op,
    activities.WithParameters(data.Output, outParam))
```

Create the DataObject over the same id, then associate the task as its source —

```go
resDO, err := dataobjects.New(name+"-result",
    data.MustItemDefinition(
        values.NewVariable(""),
        foundation.WithID(resID)),
    nil)

// task output (resID) → DataObject
err = resDO.AssociateSource(st, []string{resID}, nil)
```

Register the DataObject on the process alongside the flow nodes, so every
instance seeds its own copy into scope —

```go
for _, e := range []flow.Element{
    start, split, greetA, greetB, endA, endB, resultA, resultB,
} {
    _ = proc.Add(e)
}
```

## Run it

```bash
cd examples/process-data && go run .
```

Two parallel branches each produce a greeting and land it in their own
per-instance DataObject; the program reads each one back by name through the
instance handle:

```
  ▶ greet-a produced "Hello, dr.Dobermann!" (instance started 2026-07-27 …)
  ▶ greet-b produced "Welcome, dr.Dobermann!" (instance started 2026-07-27 …)
  ✓ greet-a-result = "Hello, dr.Dobermann!"
  ✓ greet-b-result = "Welcome, dr.Dobermann!"
✓ data-demo completed: the property fed both branches through their frames;
  each result reached its per-instance DataObject in scope, read back by name
```

Reading a DataObject back is the ordinary by-name resolve — nothing
DataObject-specific:

```go
d, err := h.Data().GetData(res.do.Name())
got, _ := d.Value().Get(ctx).(string)
```

## Runtime behavior

The engine drives the association; a developer needs to know these:

- **Registration seeds per-instance copies.** Because the DataObject is
  `proc.Add`-ed to the process, each instance clones it (`CloneDataObjects`) into
  its own scope at start; the branch results land in *that* instance's objects,
  not a shared one. This scope-tree residency is what makes it resolvable by
  name (ADR-030 §2.1) — the difference from a `Property` is visibility, not
  mechanism.
- **The id is the wiring.** `AssociateSource(st, []string{resID}, nil)` matches
  the object's source to the task output whose id is `resID`. Mismatch the ids
  and nothing flows.
- **Commit is asynchronous.** The value reaches the object when the producing
  frame commits (its `UploadData` stage), not when the operation returns — the
  example waits for both branches, then a brief grace, before reading the
  objects back.
- **Lifecycle follows the parent scope.** A DataObject is instantiated when its
  parent scope opens and disposed when it closes (BPMN §10.4.1) — a
  Process-level object lives for the whole instance; a Sub-Process one for that
  sub-process's activation.

## DataObject vs its neighbours

| | Seeded when | Lifetime | Written by |
|---|---|---|---|
| `Property` | build time (`data.WithProperties`) | its scope | seeded, then nodes |
| **DataObject** | instance start (registered on the process) | its scope (dies with the instance) | a node, at run time, via association |
| [Data Store](data-store.md) | engine construction | engine-global, outlives every instance | a node, via the *same* association wiring |

All three are read by name through the same walk-up resolver; the association
wiring (`AssociateSource` / `AssociateTarget`) is identical for a DataObject and
a Data Store — only the residency differs.

## See also

- Full example: [`examples/process-data/`](../../../examples/process-data/)
- Related guides: [Working with data — overview](index.md) · [Item definitions](item-definitions.md) · [Data Store](data-store.md) · [Reading & writing by path](structural.md)
- Design: [ADR-030 — Data Objects & Data Store](../../design/ADR-030-data-objects-and-store.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/data_objects`
