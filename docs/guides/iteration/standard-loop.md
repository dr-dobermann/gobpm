---
title: Standard Loop
description: Sequential, condition-driven repetition.
---

# Standard Loop

A **Standard Loop** re-runs a single activity in place, one pass at a time, for
as long as a boolean condition holds — the BPMN counterpart of a `while` or
`do…while` (§13.3.6). You mark an activity with loop characteristics instead of
drawing the same task twice; the engine drives the passes and publishes a
0-based `loopCounter` the condition and the body can read (with the rest of
what a loop publishes — see
[Iteration runtime variables](runtime-variables.md)). This page is the
developer reference — the type, its constructor, the marker contract, its
options, and its runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Activity → loop characteristics → **Standard Loop** (§13.3.6) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/activities` |
| Type | `activities.StandardLoopCharacteristics` |
| Embeds | `foundation.BaseElement` (id, documentation) |
| Implements | `activities.LoopCharacteristics` (the sealed iteration marker) |
| The work | wraps any activity via `WithLoop`; drives repeated passes while `loopCondition` holds |

Where it sits in the iteration family: [Iteration taxonomy](index.md). The
sibling marker is [Multi-Instance](multi-instance.md).

## Constructor

```go
func NewStandardLoop(
    loopCondition data.FormalExpression,
    opts ...StandardLoopOption,
) (*StandardLoopCharacteristics, error)
```

| Parameter | Meaning |
|---|---|
| `loopCondition` | the boolean continuation expression. Must be non-nil and evaluate to a `bool` (§13.3.6); it reads process/scope data by name — typically the engine-published `loopCounter`. |
| `opts` | zero or more `StandardLoopOption` (below). |

It returns an error — never panics — on an invalid combination: a nil or
non-boolean condition, or a non-positive `WithLoopMaximum`, is rejected at build
time rather than surfacing as a runtime failure.

The returned value is a `LoopCharacteristics`; attach it to the activity with
`activities.WithLoop(loop)`.

## Options

Most loops need none — the bare condition post-tests each pass. Reach for these
to change the shape:

| Option | When you reach for it |
|---|---|
| `WithTestBefore()` | make it **pre-tested** (`while`): test the condition *before* the first pass, so zero iterations are possible. |
| `WithLoopMaximum(n)` | a hard cap on the pass count (`n > 0`), applied regardless of the condition — a safety net against a condition that never goes false. |

The full family is `StandardLoopOption`:

| Option | Signature | Effect |
|---|---|---|
| `WithTestBefore` | `WithTestBefore() StandardLoopOption` | pre-test the condition (`while`): checked before each run, zero iterations possible. Default is post-tested (`do…while`, §13.3.6). |
| `WithLoopMaximum` | `WithLoopMaximum(n int) StandardLoopOption` | cap the iteration count at `n` (`> 0`), regardless of `loopCondition`; when unset the loop is bounded only by the condition. |

> The activity itself is marked with the **activity** option `WithLoop(lc)`, not
> a Standard-Loop option — `WithLoop` accepts any `LoopCharacteristics`, so the
> same wiring attaches a Multi-Instance marker. An activity holds a single
> marker: a later `WithLoop` replaces an earlier one.

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/activities`.

## The LoopCharacteristics contract

`NewStandardLoop` returns a `LoopCharacteristics` — the sealed marker every
iterating activity carries:

```go
type LoopCharacteristics interface {
    // Has unexported methods.
}
```

The interface is **sealed to the package** via an unexported marker method
(ADR-025 §2.1): you don't implement it — you build one of the concrete kinds
(`NewStandardLoop` here, or the Multi-Instance constructor) and hand it to
`WithLoop`. The concrete kind selects the execution mechanism (ADR-025 §2.2);
an activity carries at most one.

## Build it

Build a `StandardLoop` from the boolean condition, then attach it to the
activity with `WithLoop`:

```go
loop, err := activities.NewStandardLoop(loopCounterBelow(3))

work, err := activities.NewServiceTask("work", op,
    activities.WithLoop(loop), activities.WithoutParams())
```

The condition is an ordinary boolean expression that reads process/scope data by
name — here it reads the engine-published `loopCounter` and returns
`loopCounter < n`:

```go
func loopCounterBelow(n int) data.FormalExpression {
    return goexpr.Must(nil,
        data.MustItemDefinition(values.NewVariable(false)),
        func(ctx context.Context, ds data.Source) (data.Value, error) {
            d, err := ds.Find(ctx, "loopCounter")
            if err != nil {
                return nil, err
            }
            v, _ := d.Value().Get(ctx).(int)
            return values.NewVariable(v < n), nil
        })
}
```

The activity body reads the same `loopCounter` by name each pass:

```go
op, _ := gooper.New("work",
    func(ctx context.Context, r service.DataReader,
        _ *data.ItemDefinition) (*data.ItemDefinition, error) {
        d, _ := r.GetData("loopCounter")
        fmt.Printf("    iteration: loopCounter=%v\n", d.Value().Get(ctx))
        return nil, nil
    })
```

## Run it

```bash
cd examples/standard-loop && go run .
```

The task runs three times (`loopCounter` 0, 1, 2); after pass 2 the counter is 3,
`loopCounter < 3` goes false, and the instance completes:

```
    iteration: loopCounter=0
    iteration: loopCounter=1
    iteration: loopCounter=2
  process completed — the loop ran to its condition.
```

## Methods & runtime behavior

The marker is a value object — you read it, the engine drives it:

| Method | Role |
|---|---|
| `LoopCondition() data.FormalExpression` | the boolean continuation expression. |
| `LoopMaximum() (int, bool)` | the iteration cap and whether one is set; `ok == false` means unbounded (subject only to the condition). |
| `TestBefore() bool` | `true` for a pre-tested (`while`) loop; `false` is the post-tested `do…while` default (§13.3.6). |

Behavior worth knowing:

- **`loopCounter`** — a 0-based counter the engine publishes before each pass.
  The condition and the activity read it by name; `loopCounter < 3` runs the
  body three times.
- **Post-tested by default** — the body runs, *then* the condition is tested
  (`do…while`), so the loop runs at least once. `WithTestBefore()` flips this to
  pre-tested (`while`), where a condition already false yields zero passes.
- **In-place iteration** — a leaf activity (a Task) loops without opening a new
  scope; each pass is a fresh execution frame, which is the iteration's
  isolation. A composite (Sub-Process / Call Activity) re-opens its child scope
  per pass, so each iteration's facts carry the `loopCounter` and are
  individually observable.
- **Any activity, with one exception** — the same marker works on a Service
  Task, Sub-Process, or Call Activity. An **Event Sub-Process cannot** carry
  loop characteristics — it is instantiated by its event trigger, not reached by
  a token and iterated.

## What the passes produce

By default the last write wins, which for a loop is the useful behaviour: pass
*k* reads what pass *k-1* committed, so the loop accumulates in the enclosing
scope. That fold is the shape's whole point, and `WithLoopResultReduce(name)`
names it — it changes nothing, and exists so a model can state the intent it is
relying on rather than leaving a reader to rediscover it by experiment.

Where every pass's own result matters, declare one:

- `WithLoopResultArray(name, item)` — indexed by pass ordinal.
- `WithLoopResultMap(name, item, key, …)` — keyed by an expression evaluated in
  the completing pass's own frame.

Both are an **engine extension**: BPMN gives a Standard Loop no output
aggregation at all, only a Multi-Instance. The rules — an empty key refuses, a
duplicate overwrites unless `ErrorOnKeyRewrite`, and the assembled value
publishes once at completion — are the same either way, and are written up
under [Multi-Instance](multi-instance.md#what-the-iterations-produce).

## Restarts

A Standard-Loop **composite** restored mid-flight resumes at its
recorded pass (the position rides the instance checkpoint, ADR-033
v.4 §2.10); completed passes never re-run, and the loop condition
re-evaluates naturally at the resumed pass over the restored data.


## See also

- Examples: [`examples/standard-loop/`](../../../examples/standard-loop/)
- Related guides: [Multi-Instance](multi-instance.md) — collection fan-out, sequential or parallel · [Service Task](../tasks/service-task.md) · [Expressions](../data/expressions.md)
- Design: [ADR-025 — activity iteration: loop and multi-instance](../../design/ADR-025-activity-iteration-loop-and-multi-instance.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/activities`
