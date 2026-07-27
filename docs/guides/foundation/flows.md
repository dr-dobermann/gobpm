---
title: Sequence flows & associations
description: Connecting nodes — flow.Link, conditions, default flows, and data/artifact associations, with the shared Node and Element contracts every element carries.
---

# Sequence flows & associations

Nodes don't run in isolation — a process is a *graph*. A **sequence flow** is
the directed edge that orders two nodes ("after start, go to the gateway");
an **association** is the non-ordering link that ties data or an annotation to
a node. This page is the developer reference for the connective tissue in
`pkg/model/flow`: the `SequenceFlow` type and its `Link` constructor, the
condition and default-flow mechanics that make a gateway route, and the shared
`Node` / `Element` contracts every connectable element implements.

## Taxonomy

| | |
|---|---|
| BPMN category | Connecting Objects → **Sequence Flow** (§7.4) and **Association** (§7.4, §8.3.1) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/flow` |
| Edge type | `flow.SequenceFlow` (embeds `flow.BaseElement`) |
| Constructor | `flow.Link(src, trg, opts…)` — also `CloneFlow` / `MustCloneFlow` for snapshot cloning |
| Endpoints | a `flow.SequenceSource` (outgoing) → a `flow.SequenceTarget` (incoming) |
| Data association | `data.Association` — moves a value along a data edge |
| Artifact association | `artifacts.Association` — ties an annotation/artifact to a flow object |

Only Gateways, Activities, and Events can be sequence-flow endpoints — they are
the node families that embed `flow.BaseNode`. See the family pages:
[Activities](../tasks/index.md) · [Gateways](../gateways/index.md) ·
[Events](../events/index.md).

## Connecting two nodes — `Link`

`Link` is the one call you use to draw an edge. It creates the `SequenceFlow`,
registers it on both endpoints, and — when the source lives in a container —
adds the flow to that same container so you don't wire it in twice.

```go
func Link(
    src SequenceSource,
    trg SequenceTarget,
    flowOptions ...options.Option,
) (*SequenceFlow, error)
```

| Parameter | Meaning |
|---|---|
| `src` | the source node — must implement `SequenceSource` (any `flow.BaseNode`-based element). |
| `trg` | the target node — must implement `SequenceTarget`. |
| `flowOptions` | zero or more options (below). |

It returns an error — never panics — when an endpoint rejects the flow (via
`SupportOutgoingFlow` / `AcceptIncomingFlow`) or an option is invalid.

The options `Link` accepts:

| Option | Effect |
|---|---|
| `flow.WithCondition(cond data.FormalExpression)` | attach a boolean guard — the flow is taken only when `cond` evaluates true (gateway routing). |
| `options.WithName(name)` | name the edge (its diagram label). |
| `foundation.WithID(id)` | pin the flow's id instead of a generated one. |
| `foundation.WithDoc(...)` | attach documentation. |

From `examples/gateway-routing/process.go` — an unconditional edge, then a
conditional one, then the plain edge that becomes the gateway default:

```go
if _, err := flow.Link(start, xor); err != nil {
    return nil, fmt.Errorf("link start->xor: %w", err)
}

if _, err := flow.Link(xor, review,
    flow.WithCondition(amountGt1000())); err != nil {
    return nil, fmt.Errorf("link xor->review: %w", err)
}

df, err := flow.Link(xor, approve)      // no condition — the fallback edge
```

## Conditions & default flows

A gateway decides which outgoing edge(s) a token takes by evaluating each
flow's **condition**. The two pieces:

- **`flow.WithCondition(expr)`** stores a `data.FormalExpression` on the edge;
  `SequenceFlow.Condition()` reads it back. An edge with no condition is
  unconditional. In the example, `amountGt1000()` returns a `goexpr` expression
  that reads the `amount` property and yields `amount > 1000`.
- **The default flow** is the edge taken when *no* conditional edge is true. It
  is not an option on the edge — it is registered on the gateway through the
  `flow.DefaultFlowHolder` contract (below), by handing it the already-created
  `*SequenceFlow`.

```go
type DefaultFlowHolder interface {
    Node

    DefaultFlow() *SequenceFlow
    UpdateDefaultFlow(f *SequenceFlow) error
}
```

Gateways implement `DefaultFlowHolder`; you call `UpdateDefaultFlow` with a
flow you already `Link`ed:

```go
df, err := flow.Link(xor, approve)
// …
if err := xor.UpdateDefaultFlow(df); err != nil {
    return nil, fmt.Errorf("set default flow: %w", err)
}
```

> Evaluation order is stable: `BaseNode` holds flows in a declaration-ordered
> slice, so `Outgoing()` returns them in the order you `Link`ed them — the
> exclusive first-true rule (ADR-005 §2.8) depends on it. Register the default
> as a plain (condition-less) edge and let `UpdateDefaultFlow` mark it.

### Run it

`cd examples/gateway-routing && go run .` — `amount = 2500`, so the
`amount > 1000` condition wins and the default `auto-approve` edge is skipped:

```
order amount = 2500
  ▶ amount > 1000 → routed to manager review
✓ gateway-routing completed (Completed): the exclusive gateway chose the branch by data
```

## The `SequenceFlow` type

`SequenceFlow` embeds `flow.BaseElement` (so it carries an id, name,
documentation, and a container binding) and exposes its endpoints and guard:

| Method | Role |
|---|---|
| `Source() SequenceSource` | the node the edge leaves. |
| `Target() SequenceTarget` | the node the edge enters. |
| `Condition() data.FormalExpression` | the guard expression, or nil if unconditional. |
| `EType() ElementType` | the element kind — `SequenceBaseElement`. |
| `Validate() error` | endpoint / same-container invariants (both ends bound to the same container). |

Two more constructors exist for snapshot cloning — you rarely call them, the
engine does: `CloneFlow(orig, src, trg)` copies an edge onto freshly-cloned
endpoints, and `MustCloneFlow` is its panicking twin.

## The connectable-node contract

Every element that can sit on a sequence flow implements `flow.Node` (via the
embedded `flow.BaseNode`). This is the shared base every activity, gateway, and
event carries — the reason they can all be wired the same way.

```go
type Node interface {
    Element

    Incoming() []*SequenceFlow
    Outgoing() []*SequenceFlow
    AddFlow(*SequenceFlow, data.Direction) error
    NodeType() NodeType
    Node() Node
    Clone() (Node, error)
}
```

| Member | Role |
|---|---|
| `Incoming()` / `Outgoing()` | the edges in/out, in declaration order. |
| `AddFlow(sf, dir)` | register an edge on a direction (`Link` calls this for you). |
| `NodeType()` | the node family — `ActivityNodeType`, `EventNodeType`, `GatewayNodeType`. |
| `Node()` | the underlying node (identity through embedding). |
| `Clone()` | a per-instance copy — config shared by reference, flow collections empty for re-wiring. |

`Node` embeds `flow.Element` — the flow-graph super-type that adds identity,
naming, and container membership:

```go
type Element interface {
    foundation.BaseObject
    foundation.Namer

    Container() Container
    EType() ElementType
    BindTo(Container) error
    Unbind() error
}
```

To be a flow *endpoint* a node also implements one or both directional
interfaces — each adds a single acceptance hook the endpoint uses to accept or
reject an edge:

```go
type SequenceSource interface {
    Node
    SupportOutgoingFlow(sf *SequenceFlow) error
}

type SequenceTarget interface {
    Node
    AcceptIncomingFlow(sf *SequenceFlow) error
}
```

## Associations

A **sequence flow orders execution**; an **association does not**. There are two
distinct association types, in two packages — don't confuse them:

| Type | Package | Purpose |
|---|---|---|
| `data.Association` | `pkg/model/data` | a **data association** — moves an item's value into a node's input or out of its output. |
| `artifacts.Association` | `pkg/model/artifacts` | a **visual association** (BPMN Artifact) — ties a text annotation or artifact to a flow object; carries no runtime value. |

Data associations are how a node consumes/produces named data; the source and
target sides are declared by the `flow.AssociationSource` / `AssociationTarget`
contracts a node implements:

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

A `data.Association` is built with `data.NewAssociation(target, opts…)` and
resolves its value through `Value(ctx)` / `Find(ctx, name)`. The data plane —
item definitions, item-aware elements, and how associations move values — is
covered on its own pages: [Item definitions & item-aware
elements](../data/item-definitions.md) and [Reading & writing by
path](../data/structural.md).

The `artifacts.Association` is a plain struct (`Source`, `Target`, `Direction`)
used for diagram-level annotations and to point a compensation activity at what
it compensates; it does not participate in token flow.

## See also

- Examples: `examples/gateway-routing/` (conditions + default flow)
- Related guides: [Foundation elements](index.md) · [Exclusive gateway](../gateways/exclusive.md) · [Item definitions](../data/item-definitions.md)
- Design: [ADR-005 — gateways & joins](../../design/ADR-005-gateways-and-joins.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/flow`
