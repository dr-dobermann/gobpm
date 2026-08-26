---
title: Multi-Instance
description: Fan an activity over a collection, sequentially or in parallel.
---

# Multi-Instance

A **Multi-Instance** marker runs an activity a *fixed* number of times — one
instance per element of a collection, decided once at activation. It is the
collection fan-out counterpart of the condition-driven
[Standard Loop](standard-loop.md): reach for it when you have N items and want
the same activity applied to each, either one after another (**sequential**) or
all at once (**parallel**). This page is the developer reference — the type, its
constructor, every option, and its runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Activity → Loop characteristics → **Multi-Instance** (§13.3.7) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/activities` |
| Type | `activities.MultiInstanceLoopCharacteristics` |
| Embeds | `foundation.BaseElement` |
| Implements | `activities.LoopCharacteristics` (the sealed loop marker) |
| Attached with | `activities.WithLoop(lc)` — an `ActivityOption` on any activity |
| The work | the marked activity, run once per collection element |

Where it sits in the family: [Iteration taxonomy](index.md).

## Constructor

```go
func NewMultiInstance(
    opts ...MultiInstanceOption,
) (*MultiInstanceLoopCharacteristics, error)
```

| Parameter | Meaning |
|---|---|
| `opts` | the Multi-Instance options (below); at minimum, exactly one cardinality source. |

It returns an error — never panics — when the option set is invalid. The
constructor **requires exactly one cardinality source** (`WithCardinality`
**XOR** `WithInputCollection`), an integer cardinality expression, and — when
present — a boolean `completionCondition`. Build the marker, then hand it to the
activity with `WithLoop`:

```go
mi, _ := activities.NewMultiInstance(
    activities.WithSequential(),
    activities.WithInputCollection("amounts", "amount"),
    activities.WithOutputCollection("taxed", "withTax"))
body, _ := activities.NewSubProcess("orders", activities.WithLoop(mi))
```

> `WithLoop` replaces any earlier loop marker — an activity carries **at most
> one** (`LoopCharacteristics` is sealed to the package).

## Options

Most Multi-Instance markers need only these:

| Option | When you reach for it |
|---|---|
| `WithInputCollection(ref, item)` | count = size of the `ref` collection; bind each element to `item`. |
| `WithOutputCollection(ref, item)` | assemble each instance's `item` output — positionally — into `ref`. |
| `WithSequential()` | run instances one at a time; omit for parallel (the §13.3.7 default). |
| `WithCompletionCondition(expr)` | stop early once a boolean holds after an instance completes. |

The full family, from `MultiInstanceOption`:

| Option | Effect |
|---|---|
| `WithCardinality(expr data.FormalExpression)` | count from an integer expression (XOR the input collection). |
| `WithInputCollection(ref, item string)` | count = collection size; element *i* bound to `item` in instance *i*'s scope. |
| `WithOutputCollection(ref, item string)` | assemble each instance's `item` into `ref`, in input order. |
| `WithSequential()` | sequential execution; instance *i+1* opens only after *i* drains. |
| `WithCompletionCondition(expr data.FormalExpression)` | boolean re-evaluated after each completion; `true` finishes the activity now. |
| `WithBehavior(b MultiInstanceBehavior)` | select the completion-event behavior (below). |
| `WithComplexBehavior(defs ...*ComplexBehaviorDefinition)` | the `BehaviorComplex` entries. |
| `WithNoneBehaviorEvent(def flow.EventDefinition)` | the event thrown by `BehaviorNone`. |
| `WithOneBehaviorEvent(def flow.EventDefinition)` | the event thrown by `BehaviorOne`. |

`MultiInstanceBehavior` governs whether an event is thrown as instances complete
(§13.3.7, [ADR-025](../../design/ADR-025-activity-iteration-loop-and-multi-instance.md) §2.8):

| Constant | Throws |
|---|---|
| `BehaviorAll` | nothing — the default, zero-cost case. |
| `BehaviorNone` | `noneBehaviorEventRef` on **every** completion. |
| `BehaviorOne` | `oneBehaviorEventRef` once, on the **first** completion. |
| `BehaviorComplex` | consults each `ComplexBehaviorDefinition`; throws those whose condition holds. |

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/activities`.

## Build it

Seed the input collection as a process property:

```go
proc, _ := process.New("multi-instance-sequential",
    data.WithProperties(data.MustProperty("amounts",
        data.MustItemDefinition(values.NewArray(100, 250, 80),
            foundation.WithID("amounts")),
        data.ReadyDataState)))
```

The body's task reads its per-instance `amount` **by name** and returns the
`withTax` item the marker assembles into `taxed`:

```go
op, _ := gooper.New("tax",
    func(ctx context.Context, r service.DataReader,
        _ *data.ItemDefinition) (*data.ItemDefinition, error) {
        d, _ := r.GetData("amount")            // this instance's element
        amount, _ := d.Value().Get(ctx).(int)
        withTax := amount + amount/5           // +20%
        fmt.Printf("    order: amount=%d → withTax=%d\n", amount, withTax)
        return data.MustItemDefinition(
            values.NewVariable(withTax), foundation.WithID("withTax")), nil
    })
task, _ := activities.NewServiceTask("tax", op, activities.WithoutParams())
```

## Run it

```bash
cd examples/multi-instance-sequential && go run .
```

The three instances run in order, and the assembled collection appears only
after the last one drains — the *visibility barrier*:

```
    order: amount=100 → withTax=120
    order: amount=250 → withTax=300
    order: amount=80 → withTax=96

  completed — taxed amounts: [120 300 96]
```

## Execution modes

**Sequential** (`WithSequential()`) — instance *i+1* opens only after *i*
drains. `WithCompletionCondition` here simply *stops launching* the remaining
instances.

**Parallel** (the default — omit `WithSequential`) — all N instances start at
activation, each in its **own child scope**, and the activity completes when the
last drains. Print order varies run to run, yet positional assembly keeps the
output in input order. `WithCompletionCondition` here **cancels** the
still-running instances (their scopes torn down, counted in
`numberOfTerminatedInstances`). See
[`examples/multi-instance-parallel/`](../../../examples/multi-instance-parallel/):

```go
mi, _ := activities.NewMultiInstance(
    // no WithSequential → parallel (the §13.3.7 default)
    activities.WithInputCollection("reviewers", "reviewer"),
    activities.WithOutputCollection("scores", "score"))
```

## The behavior contract

A Multi-Instance can throw a **boundary-catchable** event as instances complete
— a progress signal — via `WithBehavior` + a `ComplexBehaviorDefinition`. The
event is a `*events.ImplicitThrowEvent` (never reached by a token; emitted by
the engine), and the condition is any boolean `data.FormalExpression`:

```go
func NewComplexBehaviorDefinition(
    condition data.FormalExpression,
    event *events.ImplicitThrowEvent,
) (*ComplexBehaviorDefinition, error)
```

Wire a quorum signal that fires once two reviewers have voted
([`examples/multi-instance-behavior/`](../../../examples/multi-instance-behavior/)):

```go
quorum, _ := events.NewImplicitThrowEvent("quorum", throwDef)
cbd, _ := activities.NewComplexBehaviorDefinition(completedAtLeast(2), quorum)
mi, _ := activities.NewMultiInstance(
    activities.WithInputCollection("reviewers", "reviewer"),
    activities.WithBehavior(activities.BehaviorComplex),
    activities.WithComplexBehavior(cbd))
```

The condition reads a runtime attribute (below) at the host scope; the thrown
signal is caught by a boundary event on the activity — interrupting (cancels the
activity) or non-interrupting (a notification; the activity continues).

## Methods & runtime behavior

The marker is a read-only descriptor once built; the engine drives the iteration
and consults these accessors:

| Method | Reports |
|---|---|
| `IsSequential() bool` | sequential vs parallel. |
| `LoopCardinality()` / `LoopDataInputRef()` | the cardinality source in use. |
| `InputDataItem()` / `OutputDataItem()` | the per-instance element / result names. |
| `LoopDataOutputRef()` | the output collection ref. |
| `CompletionCondition()` | the early-stop boolean, if any. |
| `Behavior()` / `ComplexBehavior()` | the completion-event behavior + its entries. |
| `NoneBehaviorEvent()` / `OneBehaviorEvent()` | the `BehaviorNone` / `BehaviorOne` events. |

Behavior worth knowing:

- **Cardinality is fixed at activation** from exactly one source — an integer
  `WithCardinality(expr)` or the size of `WithInputCollection(ref, item)`.
- **The output collection is assembled positionally** (output slot = input
  ordinal), so the result is deterministic even when parallel instances complete
  out of order. It is published **once** at completion — never visible mid-run.
- **Each instance publishes runtime attributes** readable by name:
  `loopCounter` and the engine's `ITERATION_NUMBER` / `ITERATION_ID` /
  `ITERATION_MODE` are the *instance's own*; `numberOfInstances`,
  `numberOfActiveInstances`, `numberOfCompletedInstances` and
  `numberOfTerminatedInstances` belong to the activity. All of them end with
  the activity; `RUNTIME/ITERATIONS` is what a later node reads. The full
  table, with addresses and lifetimes, is
  [Iteration runtime variables](runtime-variables.md) — and note the names are
  reserved: a model declaring one is refused at build time.

> The marker works on any activity, but a composite (Sub-Process / Call
> Activity) **opens a child scope per instance**, so the iterations are
> individually observable. To watch progress, read a runtime attribute or throw
> a behavior event — reading the output collection mid-run sees nothing until
> the barrier lifts. An Event Sub-Process cannot carry Multi-Instance: it is
> instantiated by its trigger, not reached by a token and iterated.

## Leaf activities under Multi-Instance

MI decorates ANY activity, and since SRD-086 a leaf means what it
declares: a **sequential leaf** re-runs the task in place — each pass
in a fresh frame with its split item and `loopCounter` — and a
**parallel leaf** fans out per-instance scopes, each running one track
at the task. Before SRD-086 a leaf MI silently executed ONCE.

**A leaf that WAITS can be iterated** (SRD-090.B). The decorator owns the
node's event registration across iterations, so it is the single event
processor for the activity: it holds one subscription per definition and
routes each delivery to the instance that was waiting for it. A sequential
Multi-Instance over a `ReceiveTask` consumes one message per pass, and a
Standard Loop over one does the same.

Two shapes are still refused at `snapshot.New`, and for different reasons:

- **A parallel fan-out over a Message catch with no iteration correlation.**
  A message is point-to-point, so with N instances waiting at once nothing
  says which envelope belongs to which. Declare
  `activities.WithIterationCorrelation` (see *Events in a parallel body*)
  and it builds; leave it out and any choice would be a coin toss.
- **A parallel fan-out over work that parks outside the event system** — a
  User Task or an external-worker Service Task. Those park on a capability
  rather than a subscription, and the identity that addresses the parked
  work is one slot on the host track, so N instances would announce a single
  task between them: the rest would complete without anyone doing them.
  Make it **sequential** — one instance parks at a time and each pass is
  completed on its own — or model N tasks.

```go
// works: one message consumed per pass
recv, _ := activities.NewReceiveTask("collect", msg,
    activities.WithoutParams(), activities.WithLoop(seqMI))

// works: a parallel fan-out that says how its envelopes are addressed
recv, _ := activities.NewReceiveTask("collect", msg,
    activities.WithoutParams(), activities.WithLoop(parallelMI),
    activities.WithIterationCorrelation("iterKey", iterExpr))

// refused: a parallel fan-out over a User Task
ut, _ := activities.NewUserTask("approve",
    activities.WithCandidateUsers("alice"),
    activities.WithoutParams(), activities.WithLoop(parallelMI))
```

The parallel human fan-out is designed — [ADR-025](../../design/ADR-025-activity-iteration-loop-and-multi-instance.md)
§2.15 and [ADR-020](../../design/ADR-020-human-interaction-execution-model.md)
§2.12 decide what it means — and the refusal lifts when each instance owns
its parked identity.

## Events in a parallel body

Parallel iterations execute over ONE shared node graph, so a catch
inside the body is the SAME node for every iteration. Two rules keep
that sharing safe (ADR-006 v.5 §2.9):

- **The payload binds per delivery.** Each iteration captures the item
  of the delivery IT received and binds it in its own execution frame
  — iterations can never observe a sibling's payload. A signal
  broadcast wakes every waiting iteration.
- **Messages route by iteration correlation.** Declare
  `events.WithIterationCorrelation(keyName, expr)` on the catch: the
  named process correlation key derives the envelope-side value, and
  `expr` — evaluated over the iteration's scope, where the split item
  is bound — gives each iteration its own subscription value. An
  arriving message serves exactly the matching iteration; a parallel
  message catch WITHOUT the declaration is refused loudly, because
  delivery would be ambiguous.

## Restarts

The iteration position is part of the instance checkpoint (ADR-033
v.4 §2.10): a **sequential** MI restored mid-flight resumes at its
recorded pass with the outputs collected so far — completed passes
never re-run, and a fired `completionCondition` is honored; a
**parallel** MI re-opens exactly its still-open instances at their
recorded ordinals, with completed slots' outputs (and canceled slots'
holes) intact in the assembled output.


## See also

- Examples: [`multi-instance-sequential/`](../../../examples/multi-instance-sequential/) · [`multi-instance-parallel/`](../../../examples/multi-instance-parallel/) · [`multi-instance-behavior/`](../../../examples/multi-instance-behavior/)
- Related guides: [Standard Loop](standard-loop.md) · [Embedded Sub-Process](../subprocesses/embedded.md) · [Service Task](../tasks/service-task.md) · [Boundary events](../events/boundary.md)
- Design: [ADR-025 — Activity iteration: Loop & Multi-Instance](../../design/ADR-025-activity-iteration-loop-and-multi-instance.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/activities`
