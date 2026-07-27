---
title: Data associations
description: The data edge — how a value moves between a node's inputs/outputs and a DataObject or Data Store, with AssociateSource/AssociateTarget, routing, and transformations.
---

# Data associations

A **data association** is the non-ordering edge that moves a *value* — as
opposed to a sequence flow, which moves a *token*. It binds a node's declared
input or output to a data element (a `DataObject` or a `DataStoreReference`), so
that when the activity runs, the engine reads the source into the node's input
before it executes, and pushes the node's output to the target after. This page
is the developer reference for `data.Association` and the `AssociateSource` /
`AssociateTarget` API you actually call.

## Taxonomy

| | |
|---|---|
| BPMN category | Data Association (§10.3.1 activity I/O, §10.4.2 semantics) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/data` |
| Type | `data.Association` (embeds `flow.BaseElement`) |
| Built by | `AssociateSource` / `AssociateTarget` on the **data element** (`DataObject`, `DataStoreReference`) |
| Node endpoint | `flow.AssociationSource` (has outputs) / `flow.AssociationTarget` (has inputs) |
| Direction | `data.Input` (data → node) or `data.Output` (node → data) |

You rarely call `data.NewAssociation` directly — the data element's
`Associate*` methods build the `Association` and register it on the node.

## The two directions

Associations are **directional**, and the method name is from the *node's*
role, which is easy to get backwards — read the table:

| You call (on the DataObject / DataStoreReference) | The node is | Value flows | BPMN |
|---|---|---|---|
| `AssociateSource(node, sourceIDs, xform)` | the value **source** — it produces the value | node output → data element | **DataOutputAssociation** (Node → data) |
| `AssociateTarget(node, xform)` | the value **target** — it consumes the value | data element → node input | **DataInputAssociation** (data → Node) |

So a task that *fills* a DataObject uses `AssociateSource` (the task is the
source); a task that *reads* one uses `AssociateTarget` (the task is the target).

## Building an association

Both methods live on the data element and take the node plus an optional
transformation:

```go
func (do *DataObject) AssociateSource(
    n flow.AssociationSource,       // a node with outputs
    sourceIDs []string,             // which of the node's outputs feed this element
    transformation data.FormalExpression,
) error

func (do *DataObject) AssociateTarget(
    n flow.AssociationTarget,       // a node with inputs
    transformation data.FormalExpression,
) error
```

`DataStoreReference` exposes the identical pair. The node must implement the
matching endpoint contract:

```go
type AssociationSource interface {
    Node
    Outputs() []*data.ItemAwareElement
    BindOutgoing(oa *data.Association) error
}
type AssociationTarget interface {
    Node
    Inputs() []*data.ItemAwareElement
    BindIncoming(ia *data.Association) error
}
```

Real calls from the examples:

```go
// examples/data-store — a task output writes the engine store (output assoc):
ref.AssociateSource(writer, []string{itemID}, nil)

// …and a separate task reads it back (input assoc):
ref.AssociateTarget(reader, nil)

// examples/process-data — a task output fills a per-instance DataObject:
resDO.AssociateSource(st, []string{resID}, nil)
```

## Routing — per-instance scope vs the engine store

The same association surface serves two backings, and the association itself
carries the routing:

- A **`DataObject`** association moves the value through the **per-instance
  scope** (each instance its own copy).
- A **`DataStoreReference`** association moves it through the **engine-global
  Data Store**; the association carries the `dataStoreRef`
  (`Association.DataStoreRef()`), and the task reroute branches on it — store vs
  scope — with no endpoint type-switch.

See [Data Objects](../data/data-objects.md) and [Data Store](../data/data-store.md)
for the two backings, and [Scope & the data plane](../concepts/scope-and-data.md)
for name resolution.

## Transformation

The optional `transformation` (`data.FormalExpression`) computes the moved value
rather than copying it — the standard's `DataAssociation` transformation. Pass
`nil` for a plain copy (the common case); pass an expression to derive the
target from the source(s). See [Expressions](../data/expressions.md).

## The `Association` type

Once built, the association exposes its routing and value:

| Method | Role |
|---|---|
| `TargetName()` | the target item's name (the store key / DataObject name). |
| `SourceNames()` / `SourcesIDs()` | the source item name(s) / id(s). |
| `HasSourceID(id)` | whether `id` is one of the sources (the reroute uses this to match a node output). |
| `DataStoreRef()` | the engine-store ref, or `""` for a scope-backed (DataObject) association. |
| `Value(ctx)` / `Find(ctx, name)` | the resolved value / a named source value. |
| `IsReady()` | whether the source value is available. |

## Runtime — how a value moves

Associations are evaluated **synchronous to the activity lifecycle** (§10.4.2),
by the task's data phases:

1. **Before `Exec` — `LoadData`.** For each **input** association, the engine
   resolves the source (a DataObject by name from scope, or a Data Store value
   by key) and fills the node's input parameter. A required input that can't be
   filled fails fast — gobpm never waits for data.
2. **After `Exec` — `UploadData`.** For each **output** association, the engine
   takes the produced output and writes it to the target — the per-instance
   DataObject (an in-place scope write) or the engine Data Store (a cloned
   `Put`). The `dataStoreRef` on the association selects which.

Both phases carry the association's transformation when present. The data-plane
mechanics behind resolution live in
[How a process executes](../concepts/process-execution.md) and
[Scope & the data plane](../concepts/scope-and-data.md).

## See also

- The data backings: [Data Objects](../data/data-objects.md) · [Data Store](../data/data-store.md)
- Where associations are declared: [Service Task](../tasks/service-task.md) (its `WithParameters` I/O)
- The other edge: [Sequence flows](flows.md)
- Design: [ADR-011 — process data flow](../../design/ADR-011-process-data-flow.md) · [ADR-030 — data objects & store](../../design/ADR-030-data-objects-and-store.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/data`
