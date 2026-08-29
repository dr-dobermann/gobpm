# multi-instance-human

A **parallel Multi-Instance over a User Task**: three approvals offered at
once, each its own addressable task, each completed by its own reviewer.

```
start → approve [Multi-Instance over reviewers, parallel] → report → end
```

Run it:

```bash
go run .
```

## What it shows

**Three tasks, not one.** Each iteration of the activity announces its own
task with its own identity. That is what makes them separately claimable and
separately completable — and the activity leaves only when every one of them
has actually been done.

**Each iteration resolves its own performer.** The assignee is an expression
reading `reviewer`, the element that iteration was seeded with, so the same
model line names alice, bob and carol — one per iteration. Eligibility is
assessed once, at the announcement, and checked from that snapshot afterwards.

**A declared result keeps every answer.** Without one the default is last-wins,
so which reviewer's decision survived would depend on who happened to answer
last. `WithResultMap` keys by an expression evaluated in the completing
iteration's own frame, so every decision is kept under the name of the person
who made it:

```
decisions: map[alice:approved bob:approved carol:rejected]
```

**A later node can ask what the activity did.** `RUNTIME/ITERATIONS` says the
shape, the total and how the iterations ended; `RUNTIME/ITERATION_OWNERS` says
who did which one. Neither question can be answered by BPMN's `numberOf*`
counts, which end with the activation they describe, nor by
`RUNTIME/COMPLETED_BY`, which keys by node and so holds one answer however many
iterations ran.

```
ITERATIONS:       map[approve:{mi_parallel 3 3 0}]
ITERATION_OWNERS: map[approve:map[0:alice 1:bob 2:carol]]
```

## How it runs unattended

`inbox` stands in for the place people actually work. The engine announces each
task to it; it reads the assignee the engine resolved for that iteration, and
answers as that person — Take, Claim, Complete. The Claim is not ceremony:
completion is strict, so only the holder may complete, and a second reader
racing for the same task is refused up front rather than at submit time.

The run asserts what it demonstrates: three DISTINCT tasks were offered, and
all three decisions survive under their reviewers' names. A regression fails
the example rather than printing something plausible.

## See also

- Guide: [Multi-Instance](../../docs/guides/iteration/multi-instance.md) ·
  [Iteration runtime variables](../../docs/guides/iteration/runtime-variables.md)
- Design: [ADR-025](../../docs/design/ADR-025-activity-iteration-loop-and-multi-instance.md)
  §2.6.1, §2.15 · [ADR-020](../../docs/design/ADR-020-human-interaction-execution-model.md) §2.12
- Simpler starting points: [`usertask/`](../usertask/) (one task, console-driven),
  [`multi-instance-parallel/`](../multi-instance-parallel/) (a fan-out with no human in it)
