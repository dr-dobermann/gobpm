# Activity iteration — Standard Loop

An activity marked with **loop characteristics** runs more than once without
being duplicated in the diagram (BPMN §13.3, ADR-025). gobpm's first iteration
form is the **Standard Loop** (§13.3.6) — a sequential, condition-driven loop;
Multi-Instance (collection fan-out, sequential or parallel) follows.

## Attaching a loop

Build a `StandardLoopCharacteristics` and attach it with `WithLoop`:

```go
loop, _ := activities.NewStandardLoop(cond, // cond: a boolean FormalExpression
    activities.WithTestBefore(),            // optional: pre-tested (while)
    activities.WithLoopMaximum(10))         // optional: hard iteration cap
task, _ := activities.NewServiceTask("work", op,
    activities.WithLoop(loop), activities.WithoutParams())
```

- **`loopCondition`** (`cond`) — the loop continues while it evaluates `true`.
  It reuses the ordinary boolean-expression path, so it reads process/scope data
  by name.
- **`testBefore`** — `false` (default) is **post-tested** (`do…while`: run once,
  then test); `WithTestBefore()` makes it **pre-tested** (`while`: test first, so
  zero iterations are possible).
- **`loopMaximum`** — caps the count regardless of the condition (must be > 0).

`NewStandardLoop` validates its inputs up front: a nil or non-boolean condition,
or a non-positive maximum, is rejected.

## `loopCounter`

Each pass publishes a 0-based **`loopCounter`** that the condition and the
activity read by name (through the service data reader, or a path expression). A
`loopCounter < 3` condition therefore runs the body three times (`loopCounter`
0, 1, 2).

## Leaf vs. composite

The marker works on any activity, but the execution mechanism fits the kind
(ADR-025 §2.2):

- A **leaf** activity (Task) iterates **in place** — a fresh execution frame per
  pass *is* the iteration isolation, so no scope is opened.
- A **composite** (Sub-Process / Call Activity) **re-opens its child scope** per
  iteration; each pass's scope facts carry the `loopCounter`, so the iterations
  are individually observable.

An **Event Sub-Process cannot** carry loop characteristics — it is instantiated
by its event trigger, not reached by a token and iterated.

## Example

[`examples/standard-loop/`](../../examples/standard-loop/) — a Service Task that
loops while `loopCounter < 3`, printing the counter each pass.
