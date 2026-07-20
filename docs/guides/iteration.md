# Activity iteration — Standard Loop & Multi-Instance

An activity marked with **loop characteristics** runs more than once without
being duplicated in the diagram (BPMN §13.3, ADR-025). gobpm implements the
**Standard Loop** (§13.3.6) — a sequential, condition-driven loop — and the
**sequential Multi-Instance** (§13.3.7) — a fixed collection fan-out; parallel
Multi-Instance follows (SRD-056).

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

## Multi-Instance (sequential)

A **Multi-Instance** activity (§13.3.7, SRD-055) runs a *fixed* number of times,
decided once at activation — the collection fan-out counterpart of the
condition-driven Standard Loop. gobpm executes the **sequential** shape (one
instance after another); parallel Multi-Instance follows (SRD-056).

```go
mi, _ := activities.NewMultiInstance(
    activities.WithSequential(),                          // sequential (required today)
    activities.WithInputCollection("amounts", "amount"),  // count = len(amounts)
    activities.WithOutputCollection("taxed", "withTax"),  // assemble each output
    activities.WithCompletionCondition(cond))             // optional: stop early
sub, _ := activities.NewSubProcess("orders", activities.WithLoop(mi))
```

- **Cardinality** — the instance count is fixed at activation from either an
  integer **`WithCardinality(expr)`** *or* the size of the input collection
  (**`WithInputCollection(ref, item)`**); exactly one source is required.
- **Data mediator** — with an input collection, each instance sees element *i*
  bound to the **`item`** name. With **`WithOutputCollection(ref, item)`** each
  instance's `item` output is assembled — in order — into the output collection,
  published **once** at completion (never visible mid-run — the visibility
  barrier).
- **Runtime attributes** — each pass publishes `loopCounter`,
  `numberOfInstances`, `numberOfActiveInstances`, and
  `numberOfCompletedInstances`, readable by name like `loopCounter`.
- **`completionCondition`** — a boolean re-evaluated after each instance; `true`
  stops launching the remaining instances.

Like the Standard Loop, a Multi-Instance composite re-opens its child scope per
instance, and an Event Sub-Process cannot carry it.

## Examples

- [`examples/standard-loop/`](../../examples/standard-loop/) — a Service Task
  that loops while `loopCounter < 3`, printing the counter each pass.
- [`examples/multi-instance-sequential/`](../../examples/multi-instance-sequential/)
  — a sequential Multi-Instance Sub-Process that taxes each amount in a
  collection and assembles the results.
