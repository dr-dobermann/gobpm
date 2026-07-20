# Activity iteration — Standard Loop & Multi-Instance

An activity marked with **loop characteristics** runs more than once without
being duplicated in the diagram (BPMN §13.3, ADR-025). gobpm implements the
**Standard Loop** (§13.3.6) — a sequential, condition-driven loop — and
**Multi-Instance** (§13.3.7) — a fixed collection fan-out, **sequential or
parallel**.

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

## Multi-Instance

A **Multi-Instance** activity (§13.3.7) runs a *fixed* number of times, decided
once at activation — the collection fan-out counterpart of the condition-driven
Standard Loop. It is **sequential** (SRD-055) or **parallel** (SRD-056.A).

```go
mi, _ := activities.NewMultiInstance(
    activities.WithSequential(),                          // omit for parallel (the default)
    activities.WithInputCollection("amounts", "amount"),  // count = len(amounts)
    activities.WithOutputCollection("taxed", "withTax"),  // assemble each output
    activities.WithCompletionCondition(cond))             // optional: stop early
sub, _ := activities.NewSubProcess("orders", activities.WithLoop(mi))
```

- **Sequential vs. parallel** — `WithSequential()` runs the instances one after
  another (instance *i+1* opens only after *i* drains). Without it the
  Multi-Instance is **parallel** (the §13.3.7 default): all N instances start at
  activation and run concurrently, each in a **distinct scope**, the activity
  completing when the last drains. `numberOfActiveInstances` is `> 1` for
  parallel.

- **Cardinality** — the instance count is fixed at activation from either an
  integer **`WithCardinality(expr)`** *or* the size of the input collection
  (**`WithInputCollection(ref, item)`**); exactly one source is required.
- **Data mediator** — with an input collection, each instance sees element *i*
  bound to the **`item`** name. With **`WithOutputCollection(ref, item)`** each
  instance's `item` output is assembled — in order — into the output collection,
  published **once** at completion (never visible mid-run — the visibility
  barrier).
  For parallel each instance binds its item in its **own** scope; positional
  assembly (output slot = input ordinal) keeps the output deterministic despite
  nondeterministic completion order.
- **Runtime attributes** — each instance publishes `loopCounter`,
  `numberOfInstances`, `numberOfActiveInstances`, `numberOfCompletedInstances`,
  and (parallel) `numberOfTerminatedInstances`, readable by name.
- **`completionCondition`** — a boolean re-evaluated after each instance
  completes; `true` finishes the activity now. For **sequential** that means
  *stop launching* the rest; for **parallel** the still-running instances are
  **canceled** (their scopes torn down, counted `numberOfTerminatedInstances`;
  each keeps its pre-run output slot).

Like the Standard Loop, a Multi-Instance composite re-opens (sequential) or
opens (parallel) child scopes, and an Event Sub-Process cannot carry it.

## Examples

- [`examples/standard-loop/`](../../examples/standard-loop/) — a Service Task
  that loops while `loopCounter < 3`, printing the counter each pass.
- [`examples/multi-instance-sequential/`](../../examples/multi-instance-sequential/)
  — a sequential Multi-Instance Sub-Process that taxes each amount in a
  collection and assembles the results.
- [`examples/multi-instance-parallel/`](../../examples/multi-instance-parallel/)
  — a parallel Multi-Instance review panel: reviewers score a proposal
  concurrently, and the scores assemble in reviewer order.
