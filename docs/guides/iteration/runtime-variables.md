---
title: Iteration runtime variables
description: "What an iterating activity publishes, at which address, and for how long."
---

# Iteration runtime variables

An iterating activity publishes values you can read from expressions and task
code: which iteration is running, how many there are, how many are done. This
page is the complete list — the name, where you read it from, and how long it
lasts.

Two things decide each variable's address, and they are worth understanding
once because they explain the whole table:

- **How many answers the value has.** `loopCounter` has one answer *per
  iteration* — three parallel iterations reading it at the same moment must
  get 0, 1 and 2. `numberOfInstances` has one answer *per activity*.
  `ITERATIONS` and `ITERATION_OWNERS` have one answer *per process instance*.

  Those are three different things, so the guide keeps them apart:
  **instance** always means the *process* instance, an **iteration** is one
  pass of an iterating activity, and the **host** is the node that runs them
  (SAD-001 §10.1).
- **Who needs to read it.** A `completionCondition` is evaluated outside any
  iteration, by the host, and a node inside an iterated Sub-Process reads from
  several scopes down.

## The variables

### Per iteration — read by plain name

Bound into the running iteration, so each one sees **its own** value. Read
them as ordinary names, exactly like a task input.

| Name | Value |
|---|---|
| `loopCounter` | this iteration's 0-based ordinal (BPMN Table 10.27) |
| `ITERATION_NUMBER` | the same ordinal, under the engine's own name |
| `ITERATION_ID` | this iteration's stable identity — where it runs, which activity, which ordinal |
| `ITERATION_MODE` | `std_loop`, `mi_sequential` or `mi_parallel` |

```go
op, _ := gooper.New("review", func(
    ctx context.Context, r service.DataReader, _ *data.ItemDefinition,
) (*data.ItemDefinition, error) {
    ord, err := r.GetData("loopCounter")     // 0, 1, 2 … — this iteration's
    if err != nil {
        return nil, err
    }

    who, err := r.GetData("ITERATION_ID")    // stable across a restart
    if err != nil {
        return nil, err
    }
    …
})
```

`ITERATION_ID` is **derived**, not minted: it is assembled from the enclosing
scope path, the activity id and the ordinal. All three already survive a
checkpoint, so the identity is the same after a restart with nothing stored
for it — which is what makes it safe to use as a key.

### Per activity — read by plain name

Published at the activity's own scope, so everything inside the activity sees
them, including a `completionCondition` and the body of an iterated
Sub-Process.

| Name | Value |
|---|---|
| `numberOfInstances` | the total, fixed at activation |
| `numberOfActiveInstances` | how many are running now |
| `numberOfCompletedInstances` | how many have completed |
| `numberOfTerminatedInstances` | how many a completion condition cancelled |

```go
// "stop once three reviewers have approved"
cond := goexpr.Must(nil, data.MustItemDefinition(values.NewVariable(false)),
    func(ctx context.Context, ds data.Source) (data.Value, error) {
        d, err := ds.Find(ctx, "numberOfCompletedInstances")
        if err != nil {
            return nil, err
        }

        n, _ := d.Value().Get(ctx).(int)

        return values.NewVariable(n >= 3), nil
    })

mi, _ := activities.NewMultiInstance(
    activities.WithInputCollection("reviewers", "reviewer"),
    activities.WithCompletionCondition(cond))
```

**These end with the activity.** Once it completes there is nothing running to
count, so a later node cannot read them — see `ITERATIONS` below for the
question that outlives the activity.

For a **sequential** Multi-Instance, `numberOfActiveInstances` is 1 while a
pass runs and 0 between passes, per Table 10.30's cap. That means the three
counts do not sum to the total mid-run: the iterations that have not started
yet belong to no category the standard offers. At the end they do sum, because
everything is then either completed or terminated.

### Per process instance — read by path

Served from the reserved read-only `RUNTIME` source, so you read it with a
path rather than a plain name.

| Name | Value |
|---|---|
| `RUNTIME/ITERATIONS` | map: activity id → `{kind, total, completed, terminated}` |
| `RUNTIME/ITERATION_OWNERS` | map: activity id → (ordinal → the actor who completed that iteration) |

```go
d, err := r.GetData("RUNTIME/ITERATIONS")
```

**These are the ones that outlive the activity.** `ITERATIONS` answers "how
many did we process?" from any later node, and both are keyed by activity id,
so they stay unambiguous when two activities iterate at the same time — a
parallel gateway with a Multi-Instance on each arm.

`ITERATION_OWNERS` answers *who did which one*, keyed inside by the ordinal
`ITERATION_NUMBER` publishes:

```go
d, err := r.GetData("RUNTIME/ITERATION_OWNERS")
// {"approve": {"0": "alice", "1": "bob", "2": "carol"}}
```

Both ride the checkpoint, so the answer survives the instance being released
and rebuilt. That is not an edge case for either: they exist to be read AFTER
the activity, and a process that waited on anything is answering in a rebuilt
instance. A register rebuilt empty would report an activity that processed
three items as having processed none.

It is not the same question as `RUNTIME/COMPLETED_BY`, and one cannot answer
the other. `COMPLETED_BY` keys by NODE, so an iterated activity has a single
entry however many iterations ran and whoever did them — the last completion
wins and the rest are lost. Three approvals are three pieces of work by three
people. Only iterations that were actually completed by an actor appear: one
nobody did, or one the engine ran without a human, is absent rather than
present with a blank.

## The names are the engine's

All of the above are **reserved**. A model that declares a property, data
object, data store reference or activity output with one of these names is
refused when it is built, naming the element:

```go
// error: data name "loopCounter" is published by the engine and
// cannot be declared by a model
_, err := data.NewProperty("loopCounter", item, data.ReadyDataState)

// the Must* form panics on the same error, as it does for any other
// invalid declaration
p := data.MustProperty("loopCounter", item, data.ReadyDataState)
```

The refusal is deliberate and it is unconditional — the names belong to the
engine whether or not your process iterates. Without it, a declared output
named `numberOfCompletedInstances` would commit to the very scope the engine
publishes the count at, and a `completionCondition` would then stop on a
number the model chose, with the wrong answer surfacing far from its cause.
An error on the line that wrote the name is better.

A **structural field** is unaffected: `order.loopCounter` is reached through
`order` and shadows nothing, so it builds normally.

## Reading them anywhere else

Everything above is read through the normal data surfaces — `GetData` in task
code, `Find` in an expression. Nothing about them is iteration-specific at the
call site, which is the point: an iteration reads which one it is the same
way it reads any other datum.

## See also

- [Standard Loop](standard-loop.md) · [Multi-Instance](multi-instance.md)
- [Scope and data](../concepts/scope-and-data.md) — how a plain name and a
  `SOURCE/addr` path resolve differently
- Design: [ADR-025 §2.9](../../design/ADR-025-activity-iteration-loop-and-multi-instance.md)
