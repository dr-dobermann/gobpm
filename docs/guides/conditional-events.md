# Conditional events — data-driven waiting

How a gobpm process waits on **its own data** instead of an external signal
(ADR-006 v.3 §2.7, landed by SRD-048): a conditional event carries a boolean
condition over process data; the engine re-evaluates it when committed data
changes and fires on the **false→true edge** (BPMN Table 10.84).

## Positions

| Position | Behavior |
|---|---|
| Intermediate catch | The token parks until the condition turns true — including as an **event-based-gateway arm** (the first arm to fire wins the deferred choice). |
| Boundary, interrupting | Fires while the guarded activity runs: cancels it and routes the exception flow. |
| Boundary, non-interrupting | Fires without cancelling; **re-fires** only after the condition goes false and true again (a fresh edge). |
| Start Event (top-level) | **Not supported** — Table 10.84 forbids a top-level start condition to reference process data; `Process.Validate` rejects it at registration. A conditional start arrives with event Sub-Processes, where the condition legally reads the enclosing instance. |

## The condition

Any boolean `data.FormalExpression` works; the definition rejects a non-bool
result at model build:

```go
cond, _ := goexpr.New(nil,
    data.MustItemDefinition(values.NewVariable(false)),
    func(ctx context.Context, ds data.Source) (data.Value, error) {
        d, err := ds.Find(ctx, "total")
        if err != nil {
            return nil, err
        }
        v, _ := d.Value().Get(ctx).(int)
        return values.NewVariable(v > 100), nil
    },
    goexpr.WithDependencies("total"))

watch, _ := events.NewIntermediateCatchEvent("watch-total",
    events.MustConditionalEventDefinition(cond))
```

## When does it re-evaluate?

The trigger source is the **commit-diff**: a node's frame commit produces the
changed-path set (see [data.md](data.md)), and the instance re-evaluates its
armed conditionals — plus once at arm time (already-true fires immediately).
Mid-activity writes never fire a conditional; only committed changes do.

One uniform rule, no modes:

- **no `WithDependencies`** → the condition may read anything, so it
  re-evaluates on **every** non-empty commit — always correct, just
  unfiltered;
- **`WithDependencies(paths...)`** → re-evaluates only when the commit's
  changed paths **overlap** a declared one (`data.PathsOverlap` —
  segment-boundary prefix: a change at `order` affects a dependency on
  `order.total`, and vice versa);
- an explicitly **empty** declaration is rejected at construction ("depends
  on nothing" would mean never re-evaluate).

A missing declaration costs performance, never correctness. A **wrong**
declaration is the author's contract — declare exactly what the function
reads, or declare nothing.

## Worked example

[`examples/conditional-events/`](../../examples/conditional-events/) — an
order-total watcher: a parked catch released by a sibling task's commit, with
the subscription lifecycle (`Registered`/`Fired` EventFlow facts) printed by
an observer.
