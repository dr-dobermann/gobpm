---
title: Complex gateway
description: An activation-threshold join.
---

# Complex gateway

A **complex gateway** is a data-aware synchronizing join: it fires once an
**activation rule** is satisfied — typically "N of M incoming branches have
arrived" — instead of waiting for *every* branch (a parallel join) or the
*conditionally-true* subset (an OR-join). Reach for it when partial completion
is enough: two of three approvals, a quorum, a discriminator. This page is the
developer reference — the type, its constructor, the activation options, the
`Triple` building block, and its runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Gateway → **Complex Gateway** (§10.5.5) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/gateways` |
| Type | `gateways.ComplexGateway` |
| Embeds | `gateways.Gateway` (direction, default flow, `flow.BaseNode`) |
| Implements | `flow.Node` (`Node`, `Clone`, `Validate`), `exec.NodeExecutor` (`Exec`), and the activation-join contract (`Record`, `Recheck`) |
| The work | an **activation rule** — a disjunction of `Triple`s (or a bare `WithActivationThreshold`) |

Where it sits in the gateway family: [Gateways taxonomy](index.md).

## Constructor

```go
func NewComplexGateway(opts ...options.Option) (*ComplexGateway, error)
```

| Parameter | Meaning |
|---|---|
| `opts` | base options (`foundation.WithID`/`WithDoc`, `options.WithName`, `gateways.WithDirection`) **plus exactly one** activation source. |

It returns an error — never panics — on an invalid combination. The one hard
rule: supply **exactly one** activation source, `WithActivationThreshold` **xor**
`WithActivation`. For a join, add `WithDirection(gateways.Converging)`.

## Options

Most complex joins need only one activation option plus the direction:

| Option | When you reach for it |
|---|---|
| `WithActivationThreshold(n)` | a guard-less "N of M" join — fire on any `n` arrivals. |
| `WithActivation(triples…)` | a data-aware rule — pick the threshold (or required branches) by process data. |
| `WithDirection(gateways.Converging)` | make the gateway a join (the reason to build one). |

The full set comes from two families — **gateway options** (any gateway) and
**complex options** (`ComplexOption`, activation-specific):

| Gateway option | Effect |
|---|---|
| `WithDirection(dir gateways.GDirection)` | `Converging` (join) vs `Diverging` (inclusive split) vs `Unspecified`/`Mixed`. |

| Complex option | Effect |
|---|---|
| `WithActivationThreshold(n int)` | a single guard-less threshold triple ("N of M"). Mutually exclusive with `WithActivation`. |
| `WithActivation(triples ...gateways.Triple)` | explicit activation triples (a disjunction — the first to match fires). Mutually exclusive with `WithActivationThreshold`. |

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/gateways`.

## The Triple building block

A `Triple` is one disjunct of the activation rule. The join fires when a
triple's `count` incoming flows have arrived, its guard (if any) holds, and
every `WithRequired` flow is among the arrived. A bare "N of M" is a triple
with no guard and no required flows.

```go
func NewTriple(count int, opts ...TripleOption) (Triple, error)
```

| Triple option | Effect |
|---|---|
| `WithGuard(cond data.FormalExpression)` | add a process-data guard; the triple fires only when `cond` is true. A nil condition is rejected. |
| `WithRequired(incomingFlowIDs ...string)` | pin the incoming flows that must be among the arrived (gate-identity activation / discriminator). Empty ids rejected. |

`NewTriple` rejects `count < 1` and `count < len(required)` — a triple that
demands more specific gates than its arrival budget can **never** fire, so it is
caught at build time, not silently ignored.

> A bare threshold (`WithActivationThreshold(n)`) and an explicit rule
> (`WithActivation(...)`) are mutually exclusive — pass one, not both. The
> threshold is exactly a single unconditional `Triple`.

## Build it

The activation rule below is a disjunction of two guarded triples: fire on 2
arrivals for a small order, 3 for a large one. The guard is an ordinary
process-data expression over the `amount` property (from
`examples/complex-gateway/process.go`):

```go
small, _ := gateways.NewTriple(2,
    gateways.WithGuard(amountCond(func(a int) bool { return a < 1000 })))
big, _ := gateways.NewTriple(3,
    gateways.WithGuard(amountCond(func(a int) bool { return a >= 1000 })))

cg, _ := gateways.NewComplexGateway(
    gateways.WithActivation(small, big),
    gateways.WithDirection(gateways.Converging))
```

```go
func amountCond(pred func(a int) bool) data.FormalExpression {
    return goexpr.Must(
        nil,
        data.MustItemDefinition(values.NewVariable(false)),
        func(ctx context.Context, ds data.Source) (data.Value, error) {
            v, err := ds.Find(ctx, "amount")
            if err != nil {
                return nil, err
            }
            a, _ := v.Value().Get(ctx).(int)
            return values.NewVariable(pred(a)), nil
        })
}
```

An AND-split (`gateways.NewParallelGateway()`) forks all three approver tasks;
each links into the complex join. Wiring is
`start → split → each approver → join → finalize → end` (see `process.go`).

## Run it

```bash
cd examples/complex-gateway && go run .
```

The demo runs with `amount = 500`, so the small-order triple wins: all three
approvers run, but the join fires on the **2nd** arrival, and the 3rd is
consumed as a trailing token (banner and config dump elided):

```
order amount = 500 (needs 2 approvals)
  ▶ manager approved
  ▶ cfo approved
  ▶ finance approved
  ✓ order finalized
✓ complex-gateway completed (Completed): the join fired on the 2nd approval; the 3rd was consumed as a trailing token
```

> All three approvers still run — the split is a plain parallel fork. The
> threshold governs only *when the join fires* downstream; it does not cancel
> the branches that lose the race. Their tokens arrive as trailing tokens and
> are consumed.

## Methods & runtime behavior

The engine's instance loop drives the join through these — you never call them
directly:

| Method | Role |
|---|---|
| `Record(incomingFlowID, arrivingTrackID) bool` | register an arrival; report whether the gateway already fired (so the arrival is a trailing token). Makes no decision. Atomic under the gateway's own mutex. |
| `Recheck(eval, fc) (exec.Decision, error)` | the loop's activation decision — fire (survivor = last-in), abort (rule unsatisfiable), or wait. Runs after an arrival parks and on every token death. Atomic. |
| `Exec(ctx, re) ([]*flow.SequenceFlow, error)` | diverging: route through the inclusive split; converging: pass-through continuation of the survivor. The *join* decision is `Recheck`, not `Exec`. |
| `Clone` / `Validate` / `Node` | the `flow.Node` surface — deep-copy per instance, build-time validation, node handle. |

Behavior worth knowing:

- **Converging vs diverging.** `WithDirection(gateways.Converging)` makes the
  gateway a join. Diverging, a complex gateway behaves as the inclusive split —
  it forks the conditionally-true outgoing subset.
- **Activation rule.** The rule is a disjunction of `Triple`s; the join fires as
  soon as *any* triple is satisfied. Guards and reachability are read only by
  the loop (`Recheck`), never off the arriving track's goroutine — the
  single-writer discipline that keeps per-instance state race-free.
- **Fires once.** The gateway owns its per-instance arrival state under its own
  mutex and flips to *fired* on the first satisfied triple. Later arrivals are
  counted (the count is monotonic) but do not re-fire — they are trailing
  tokens.
- **Token death aborts.** Unlike the OR-join, if an incoming token dies the
  complex join **aborts** rather than firing — a death can only make a
  partial-arrival count unsatisfiable, never newly satisfy it.

## See also

- Examples: `examples/complex-gateway/`
- Related guides: [Parallel (AND)](parallel.md) · [Inclusive (OR)](inclusive.md) · [Exclusive (XOR)](exclusive.md) · [Event-based](event-based.md)
- Design: [ADR-005 — gateways and joins](../../design/ADR-005-gateways-and-joins.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/gateways`
