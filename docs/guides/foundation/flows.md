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
| Data association | `data.Association` — moves a value along a data edge ([Data associations](data-associations.md)) |
| Artifact association | `artifacts.Association` — the carried, model-only line tying an annotation or artifact to a model element (ADR-039) |

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

## Runtime — how a token traverses the graph

At build time a sequence flow is just an edge; at run time it carries a
**token**. After a node finishes executing, the engine reads its `Outgoing()`
edges and moves the token forward — and this is where the mechanics get
interesting:

- **One outgoing flow** → the token continues on the **same track** to the next
  node; no new goroutine.
- **Several outgoing flows** → the token **splits**. The **first** edge stays on
  the current track (its next step); each remaining edge **forks a new track**
  (one per edge), run concurrently. A **cyclic** edge back to the node itself is
  preferred as the first (continuing) edge, so a self-loop stays on its own track
  instead of spawning one.
- **No outgoing flow** → the track **ends** and its token is consumed.

Which edges actually receive a token depends on the node:

- a plain node (activity/event) with several outgoing edges is an **implicit
  parallel split** — every edge is taken;
- a **gateway** filters first — an [Exclusive](../gateways/exclusive.md) gateway
  takes the first true edge (then its default), an
  [Inclusive](../gateways/inclusive.md) every true edge, a
  [Parallel](../gateways/parallel.md) all of them.

Condition evaluation is declaration-ordered (the `Outgoing()` slice above), so
the exclusive first-true rule is deterministic. Forked tracks re-converge at a
**synchronizing join** — see [Process, instance, track,
token](../concepts/execution-model.md) and the gateway pages.

## Associations

A **sequence flow orders execution**; an **association does not**. Two distinct
association types live in two packages — don't confuse them:

| Type | Package | Purpose |
|---|---|---|
| `data.Association` | `pkg/model/data` | a **data association** — moves an item's value into a node's input or out of its output. Covered in full on **[Data associations](data-associations.md)**. |
| `artifacts.Association` | `pkg/model/artifacts` | a **visual association** (BPMN §8.4.1 Artifact) — built with `NewAssociation(source, target, direction)` over any two model elements and carried in a container's artifact collection (`AddArtifacts`/`Artifacts`), model-only per ADR-039: no runtime value, no part in token flow. The tag's *compensation* shape is **not** this type — it is realized as the boundary event's handler wiring. |

## See also

- Examples: `examples/gateway-routing/` (conditions + default flow)
- Related guides: [Data associations](data-associations.md) · [Foundation elements](index.md) · [Process, instance, track, token](../concepts/execution-model.md) · [Exclusive gateway](../gateways/exclusive.md)
- Design: [ADR-005 — gateways & joins](../../design/ADR-005-gateways-and-joins.md) · [ADR-001 — execution model](../../design/ADR-001-execution-model.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/flow`
