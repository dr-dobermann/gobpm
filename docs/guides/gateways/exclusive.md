---
title: Exclusive gateway (XOR)
description: First-true routing with a default flow.
---

# Exclusive gateway (XOR)

An **exclusive gateway** sends a token down exactly **one** outgoing flow. On a
diverging split it evaluates each branch's condition in order and takes the
**first true** one; if none is true it takes the **default flow**. Reach for it
when a single data value decides which of several mutually exclusive paths a
process should follow. This page is the developer reference — the type, its
constructor, options, how it decides, and its runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Gateway → **Exclusive Gateway** (§13.3.2) — data-based XOR decision |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/gateways` |
| Type | `gateways.ExclusiveGateway` (embeds `gateways.Gateway`) |
| Inherits | the `Gateway` attributes — direction, default flow, the shared flow-condition machinery |
| Implements | `flow.Node`, `exec.NodeExecutor` (`Exec`), `Clone` |
| The work | pick the **first** outgoing flow whose condition is true, else the **default** flow |

Where it sits in the gateway family: [Gateways taxonomy](index.md).

## Constructor

```go
func NewExclusiveGateway(opts ...options.Option) (*ExclusiveGateway, error)
```

| Parameter | Meaning |
|---|---|
| `opts` | zero or more gateway options (below) — an exclusive gateway needs none for the common case. |

It returns an error — never panics — if an option rejects (for example an
invalid direction). The routing itself (conditions, default) is configured on
the **outgoing flows**, not the constructor.

## Options

Most exclusive gateways need **no options at all** — you construct the gateway
and then configure routing on the flows leaving it:

| Option | When you reach for it |
|---|---|
| *(none)* | the common case — the split has explicit incoming/outgoing flows and its conditions live on the flows. |
| `WithDirection(dir)` | pin the gateway's direction (`Diverging` to split, `Converging` to merge) instead of leaving it `Unspecified`. |

The full option set — all four accepted by `NewExclusiveGateway`:

| Gateway option | Effect |
|---|---|
| `foundation.WithID(id string)` | set an explicit element id (otherwise derived). |
| `foundation.WithDoc(doc, format)` | attach documentation. |
| `options.WithName(name string)` | set the gateway's diagram name. |
| `gateways.WithDirection(dir GDirection)` | set direction — one of `Unspecified` (default), `Converging`, `Diverging`, `Mixed`. |

> Routing is **flow-level**, not gateway-level. A branch's condition is set with
> `flow.WithCondition` on the `flow.Link` call; the fall-through branch is
> nominated as the default with the gateway's `UpdateDefaultFlow` method — see
> [Sequence flows](../foundation/flows.md).

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/gateways`.

## Routing configuration

An exclusive split is configured through the flows that leave it, not through
constructor options. Two pieces make it route:

| Piece | Call | Role |
|---|---|---|
| Branch condition | `flow.Link(xor, target, flow.WithCondition(expr))` | a boolean `data.FormalExpression` over process data — the branch is taken when it evaluates true. |
| Default flow | `xor.UpdateDefaultFlow(df)` | nominate one **conditionless** outgoing flow as the fall-through, taken only when no condition matched. |

`UpdateDefaultFlow` validates: the flow must be one of the gateway's actual
outgoing flows (else `there is no outgoing flow #…`), and it must **not** carry a
condition (else `default flow shouldn't have a condition expression`). Passing
`nil` clears the default.

## Build it

Give the process a property for the condition to read, then create the gateway.
No options are needed — the routing lives on the flows:

```go
proc, err := process.New("order-routing",
    data.WithProperties(
        data.MustProperty("amount",
            data.MustItemDefinition(
                values.NewVariable(amount),
                foundation.WithID("amount")),
            data.ReadyDataState)))

xor, err := gateways.NewExclusiveGateway()
```

Wire the branches. A conditional flow carries `flow.WithCondition`; the
fall-through flow carries no condition and is registered as the **default** with
`UpdateDefaultFlow`:

```go
if _, err := flow.Link(xor, review,
    flow.WithCondition(amountGt1000())); err != nil {
    return nil, fmt.Errorf("link xor->review: %w", err)
}

df, err := flow.Link(xor, approve)
if err != nil {
    return nil, fmt.Errorf("link xor->approve: %w", err)
}

if err := xor.UpdateDefaultFlow(df); err != nil {
    return nil, fmt.Errorf("set default flow: %w", err)
}
```

The condition is a `data.FormalExpression` that reads a named value from the
data source and returns a boolean — `ds.Find(ctx, "amount")` resolves the
process property **by name**, the same way a task reads data:

```go
func amountGt1000() data.FormalExpression {
    return goexpr.Must(
        nil,
        data.MustItemDefinition(values.NewVariable(false)),
        func(ctx context.Context, ds data.Source) (data.Value, error) {
            v, err := ds.Find(ctx, "amount")
            if err != nil {
                return nil, err
            }

            amount, _ := v.Value().Get(ctx).(int)

            return values.NewVariable(amount > 1000), nil
        })
}
```

## Run it

```bash
cd examples/gateway-routing && go run .
```

The demo runs with `amount = 2500`, so the gateway takes the manager-review
branch and the instance completes:

```
order amount = 2500
  ▶ amount > 1000 → routed to manager review
✓ gateway-routing completed (Completed): the exclusive gateway chose the branch by data
```

Set `amount` at or below 1000 in `main.go` and the token falls through to the
auto-approve (default) branch instead.

## Methods & runtime behavior

The engine drives the gateway through these — you configure routing, then rarely
call them directly:

| Method | Role |
|---|---|
| `Exec(ctx, re) ([]*flow.SequenceFlow, error)` | evaluate the outgoing flows and return the **single** chosen flow (or the default). |
| `UpdateDefaultFlow(f)` / `MustUpdateDefaultFlow(f)` | nominate (or clear) the conditionless fall-through flow; the `Must*` form panics on an invalid flow. |
| `DefaultFlow()` | the currently registered default flow, or `nil`. |
| `Direction()` | the gateway's `GDirection`. |
| `Clone()` | per-instance copy — direction and default flow are shared by reference as immutable configuration. |

How `Exec` decides (ADR-005 §2.8):

- With **one or zero** outgoing flows the gateway is a pass-through — the
  incoming token flows straight on; no conditions are evaluated.
- Otherwise it walks the outgoing flows in order and takes the **first** flow
  whose condition evaluates true — exactly one token leaves the gateway, never
  more. The remaining flows are skipped even if they would also match.
- The **default flow** is never condition-tested and is selected only when no
  other flow matched. A non-default flow **without** a condition is never
  selected.
- If no condition matched **and** there is no default flow, `Exec` returns a
  `no available outgoing flow` error — an unroutable token is a modeling error,
  so always register a default.
- Each condition is a boolean expression evaluated against the instance's data
  plane through the engine's expression engine; a non-bool result is an error.

## See also

- Examples: `examples/gateway-routing/`
- Related guides: [Parallel (AND)](parallel.md) · [Inclusive (OR)](inclusive.md) · [Complex](complex.md) · [Event-based](event-based.md) · [Sequence flows](../foundation/flows.md) · [Expressions](../data/expressions.md)
- Design: [ADR-005 — Gateways and joins](../../design/ADR-005-gateways-and-joins.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/gateways`
