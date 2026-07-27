---
title: Conditional events
description: Wait on process data; fire on the false-to-true edge.
---

# Conditional events

A conditional event waits on **your process's own data** rather than an external
message, signal, or timer. It carries a boolean condition; the engine
re-evaluates that condition whenever committed data changes and fires on the
**false→true edge**. Reach for it when a branch should pause until the state of
the instance makes a rule true — an order total crossing a threshold, a cart
filling, a flag being set — without polling. This page is the developer
reference: the definition type, its constructor, how you attach it to a catch,
the condition contract you supply, and its runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Event → Trigger → **Conditional** (§10.4.4) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/events` |
| Type | `events.ConditionalEventDefinition` |
| Trigger | `flow.TriggerConditional` (`"Conditional"`) |
| Carried by | intermediate catch, boundary (interrupting / non-interrupting), event-based-gateway arm, event-subprocess start |
| The work | a boolean `data.FormalExpression` — re-evaluated on the commit-diff |

Where it sits in the event family: [Events taxonomy](index.md).

## Constructor

A conditional event is a *definition* you hand to an event constructor. Build
the definition, then pass it to the catch (or boundary) that carries it:

```go
func NewConditionalEventDefinition(
    condition data.FormalExpression,
    baseOpts ...options.Option,
) (*ConditionalEventDefinition, error)
```

| Parameter | Meaning |
|---|---|
| `condition` | the boolean `data.FormalExpression` re-evaluated on each qualifying commit. Must be non-nil. |
| `baseOpts` | zero or more `foundation` base options (id, docs). |

It returns an error — never panics — when `condition` is invalid.
`MustConditionalEventDefinition(condition, baseOpts…)` is the panicking twin for
static process wiring.

Attach it to an intermediate catch:

```go
watch, err := events.NewIntermediateCatchEvent("watch-total",
    events.MustConditionalEventDefinition(cond))
```

For a catch built from the `EventOption` family, the option form is
`events.WithConditionalTrigger(ced)` — the same family as `WithMessageTrigger`,
`WithSignalTrigger`, `WithTimerTrigger`.

## Options

The definition itself takes only `foundation` base options; the interesting
knobs live on the **condition expression** you build with `goexpr`. Most
conditions need exactly one:

| Option | When you reach for it |
|---|---|
| `goexpr.WithDependencies(paths…)` | declare which process-data paths the condition reads, so it re-evaluates only on commits that touch them. |

`WithDependencies` filters *when* the condition is re-checked — never *whether*
it is correct:

| Condition wiring | Re-evaluation behavior |
|---|---|
| no `WithDependencies` | reads may touch anything, so it re-evaluates on **every** non-empty commit. Always correct, just unfiltered. |
| `WithDependencies(paths…)` | re-evaluates only when a commit's changed paths overlap a declared one. |
| `WithDependencies()` (empty) | **rejected at construction** — "depends on nothing" would mean "never re-evaluate", the degenerate trap. |

> A missing declaration costs performance, never correctness. A wrong
> declaration is your bug — declare exactly what the function reads, or declare
> nothing and let every commit re-check it.

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/events` and
`go doc github.com/dr-dobermann/gobpm/pkg/model/data/goexpr`.

## The condition contract

The condition is any boolean `data.FormalExpression`. Build one with
`goexpr.New(ds, res, gfunc, opts…)` — a result item that types the expression
as bool, an evaluation function reading the data you care about by name, and the
dependency declaration:

```go
func totalAbove(n int) (data.FormalExpression, error) {
    return goexpr.New(nil,
        data.MustItemDefinition(values.NewVariable(false)),
        func(ctx context.Context, ds data.Source) (data.Value, error) {
            d, err := ds.Find(ctx, "total")
            if err != nil {
                return nil, err
            }

            v, _ := d.Value().Get(ctx).(int)

            return values.NewVariable(v > n), nil
        },
        goexpr.WithDependencies("total"))
}
```

The evaluation function has the signature
`func(ctx context.Context, ds data.Source) (data.Value, error)`. It gets a
`data.Source` — resolve the property by name (`ds.Find`), read its value, and
return a boolean `data.Value`. The function must not mutate data; it is a pure
read the engine may call many times.

## Usage

The condition reads a process property that a sibling task changes. The task
returns its updated value from its operation; committing that value at the
activity boundary is the trigger the engine notices:

```go
op, _ := gooper.New(taskName,
    func(_ context.Context, _ service.DataReader,
        _ *data.ItemDefinition) (*data.ItemDefinition, error) {
        fmt.Printf("  %s → commit %s=%d\n", taskName, dataName, value)

        return data.MustItemDefinition(
            values.NewVariable(value),
            foundation.WithID(dataName)), nil
    })
```

Running `examples/conditional-events/` — `watch` arms at `total=20` so the
condition is false and the branch parks; `addItems` commits `total=140`, the
commit-diff wakes the subscription, the edge flips, and `notify` runs:

```
  prep → commit cart=1
  addItems → commit total=140
  ▶ watch-total: Registered
  ▶ watch-total: Fired
  notify → the total crossed 100, shipping upgraded
  ✓ completed (Completed)
```

The `Registered` / `Fired` lines are the watch node's `EventFlow` facts, printed
by an observer that filters on the node name:

```go
func (p *condEventPrinter) OnFact(f observability.Fact) {
    if f.Kind != observability.KindEventFlow || f.NodeName != "watch-total" {
        return
    }

    fmt.Printf("  ▶ watch-total: %s\n", f.Phase)
}
```

## Methods & runtime behavior

The definition exposes a small surface; the engine calls these — you rarely do:

| Method | Role |
|---|---|
| `Condition() data.FormalExpression` | the boolean expression the subscription re-evaluates. |
| `Type() flow.EventTrigger` | `flow.TriggerConditional` — the trigger identity. |
| `GetItemsList() []*data.ItemDefinition` | item definitions the definition is based on (empty here). |

Behavior worth knowing:

- **The trigger source is the commit-diff.** When a node's activity boundary
  commits, the engine produces the set of changed paths and re-evaluates every
  armed conditional against it — plus once at arm time, so an already-true
  condition fires immediately. Mid-activity writes never fire a conditional;
  only committed changes do.
- Conditional subscriptions are **owned by the instance loop**, the deliberate
  exception to the general event-hub subscription path — their trigger source is
  the instance's own data plane, so no cross-goroutine publish is involved.
- A conditional trigger works in several positions:

| Position | Behavior |
|---|---|
| Intermediate catch | The token parks until the condition turns true — including as an **event-based-gateway arm**, where the first arm to fire wins the deferred choice. |
| Boundary, interrupting | Fires while the guarded activity runs: cancels it and routes the exception flow. |
| Boundary, non-interrupting | Fires without cancelling; **re-fires** only after the condition goes false then true again (a fresh edge). |
| Start event (top-level) | **Not supported** — a top-level conditional start may not reference process data (§10.5.2, Table 10.84), and the engine exposes no static-process / environment surface for it to read legally; rejected at process validation. Conditional starts arrive with event sub-processes, where the condition legally reads the enclosing instance's data. |

## See also

- Example: `examples/conditional-events/`
- Related guides: [Event-based gateway](../gateways/event-based.md) · [Timer](timer.md) · [Boundary events](boundary.md) · [Expressions](../data/expressions.md)
- Design: [ADR-006 — Events & subscriptions](../../design/ADR-006-events-and-subscriptions.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/events`
