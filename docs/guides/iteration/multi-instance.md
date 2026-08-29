---
title: Multi-Instance
description: Fan an activity over a collection, sequentially or in parallel.
---

# Multi-Instance

A **Multi-Instance** marker runs an activity a *fixed* number of times — one
iteration per element of a collection, decided once at activation. It is the
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
| `WithOutputCollection(ref, item)` | assemble each iteration's `item` output — positionally — into `ref`. |
| `WithSequential()` | run iterations one at a time; omit for parallel (the §13.3.7 default). |
| `WithCompletionCondition(expr)` | stop early once a boolean holds after an iteration completes. |

The full family, from `MultiInstanceOption`:

| Option | Effect |
|---|---|
| `WithCardinality(expr data.FormalExpression)` | count from an integer expression (XOR the input collection). |
| `WithInputCollection(ref, item string)` | count = collection size; element *i* bound to `item` in iteration *i*'s scope. |
| `WithOutputCollection(ref, item string)` | assemble each iteration's `item` into `ref`, in input order. |
| `WithSequential()` | sequential execution; iteration *i+1* opens only after *i* drains. |
| `WithCompletionCondition(expr data.FormalExpression)` | boolean re-evaluated after each completion; `true` finishes the activity now. |
| `WithBehavior(b MultiInstanceBehavior)` | select the completion-event behavior (below). |
| `WithComplexBehavior(defs ...*ComplexBehaviorDefinition)` | the `BehaviorComplex` entries. |
| `WithNoneBehaviorEvent(def flow.EventDefinition)` | the event thrown by `BehaviorNone`. |
| `WithOneBehaviorEvent(def flow.EventDefinition)` | the event thrown by `BehaviorOne`. |

`MultiInstanceBehavior` governs whether an event is thrown as iterations complete
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

The body's task reads its per-iteration `amount` **by name** and returns the
`withTax` item the marker assembles into `taxed`:

```go
op, _ := gooper.New("tax",
    func(ctx context.Context, r service.DataReader,
        _ *data.ItemDefinition) (*data.ItemDefinition, error) {
        d, _ := r.GetData("amount")            // this iteration's element
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

The three iterations run in order, and the assembled collection appears only
after the last one drains — the *visibility barrier*:

```
    order: amount=100 → withTax=120
    order: amount=250 → withTax=300
    order: amount=80 → withTax=96

  completed — taxed amounts: [120 300 96]
```

## Execution modes

**Sequential** (`WithSequential()`) — iteration *i+1* opens only after *i*
drains. `WithCompletionCondition` here simply *stops launching* the remaining
iterations.

**Parallel** (the default — omit `WithSequential`) — all N iterations start at
activation, each in its **own child scope**, and the activity completes when the
last drains. Print order varies run to run, yet positional assembly keeps the
output in input order. `WithCompletionCondition` here **cancels** the
still-running iterations (their scopes torn down, counted in
`numberOfTerminatedInstances`). See
[`examples/multi-instance-parallel/`](../../../examples/multi-instance-parallel/):

```go
mi, _ := activities.NewMultiInstance(
    // no WithSequential → parallel (the §13.3.7 default)
    activities.WithInputCollection("reviewers", "reviewer"),
    activities.WithOutputCollection("scores", "score"))
```

## The behavior contract

A Multi-Instance can throw a **boundary-catchable** event as iterations complete
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
| `InputDataItem()` / `OutputDataItem()` | the per-iteration element / result names. |
| `LoopDataOutputRef()` | the output collection ref. |
| `CompletionCondition()` | the early-stop boolean, if any. |
| `Behavior()` / `ComplexBehavior()` | the completion-event behavior + its entries. |
| `NoneBehaviorEvent()` / `OneBehaviorEvent()` | the `BehaviorNone` / `BehaviorOne` events. |

Behavior worth knowing:

- **Cardinality is fixed at activation** from exactly one source — an integer
  `WithCardinality(expr)` or the size of `WithInputCollection(ref, item)`.
- **The output collection is assembled positionally** (output slot = input
  ordinal), so the result is deterministic even when parallel iterations complete
  out of order. It is published **once** at completion — never visible mid-run.
- **Each iteration publishes runtime attributes** readable by name:
  `loopCounter` and the engine's `ITERATION_NUMBER` / `ITERATION_ID` /
  `ITERATION_MODE` are the *iteration's own*; `numberOfInstances`,
  `numberOfActiveInstances`, `numberOfCompletedInstances` and
  `numberOfTerminatedInstances` belong to the activity. All of them end with
  the activity; `RUNTIME/ITERATIONS` is what a later node reads. The full
  table, with addresses and lifetimes, is
  [Iteration runtime variables](runtime-variables.md) — and note the names are
  reserved: a model declaring one is refused at build time.

> The marker works on any activity, but a composite (Sub-Process / Call
> Activity) **opens a child scope per iteration**, so the iterations are
> individually observable. To watch progress, read a runtime attribute or throw
> a behavior event — reading the output collection mid-run sees nothing until
> the barrier lifts. An Event Sub-Process cannot carry Multi-Instance: it is
> instantiated by its trigger, not reached by a token and iterated.

## Leaf activities under Multi-Instance

MI decorates ANY activity, and since SRD-086 a leaf means what it
declares: a **sequential leaf** re-runs the task in place — each pass
in a fresh frame with its split item and `loopCounter` — and a
**parallel leaf** fans out per-iteration scopes, each running one track
at the task. Before SRD-086 a leaf MI silently executed ONCE.

**A leaf that WAITS can be iterated** (SRD-090.B). The decorator owns the
node's event registration across iterations, so it is the single event
processor for the activity: it holds one subscription per definition and
routes each delivery to the iteration that was waiting for it. A sequential
Multi-Instance over a `ReceiveTask` consumes one message per pass, and a
Standard Loop over one does the same.

Two shapes are still refused at `snapshot.New`, and for different reasons:

- **A parallel fan-out over a Message catch with no iteration correlation.**
  A message is point-to-point, so with N iterations waiting at once nothing
  says which envelope belongs to which. Declare
  `activities.WithIterationCorrelation` (see *Events in a parallel body*)
  and it builds; leave it out and any choice would be a coin toss.
- **A parallel fan-out over an external-worker Service Task.** Its iterations
  would share one job identity — a job is keyed to the track it belongs to,
  with no ordinal — so a single worker report would complete work nobody
  performed. Make it **sequential** — one iteration dispatches at a time and
  each pass is reported on its own — or model N tasks. Lifting this is
  [#355](https://github.com/dr-dobermann/gobpm/issues/355).

A parallel fan-out over a **User Task** used to be refused for the same
reason, and no longer is: every iteration now owns its parked identity.

```go
// works: one message consumed per pass
recv, _ := activities.NewReceiveTask("collect", msg,
    activities.WithoutParams(), activities.WithLoop(seqMI))

// works: a parallel fan-out that says how its envelopes are addressed
recv, _ := activities.NewReceiveTask("collect", msg,
    activities.WithoutParams(), activities.WithLoop(parallelMI),
    activities.WithIterationCorrelation("iterKey", iterExpr))

// works: three approvals offered at once, each completed on its own
ut, _ := activities.NewUserTask("approve",
    activities.WithCandidateUsers("alice"),
    activities.WithoutParams(), activities.WithLoop(parallelMI))
```

## A parallel fan-out over human work

Three iterations over a collection of three offer **three tasks at once** —
each announced to the distributor with its own identity, each claimed and
completed by itself. The activity leaves only when every one of them has
actually been done: completing two of three does not finish it.

Those identities are what somebody's inbox is holding, so they survive the
process instance being released and rebuilt — and so does **who may act on
them**. Eligibility is resolved once, when the task is announced, in the data
of the iteration being announced; that verdict is what the checkpoint carries
and what every later check reads. Resolving it again on the way back would ask
the question outside the iteration, where a performer expression naming "the
reviewer this one is for" has nothing to read, and everyone holding the task
would be locked out of it.

Inside, the **host** holds the N waits and applies their completions **one at
a time, on its own goroutine**. The concurrency the construct exists for is
external — N people acting at the same time — and the iterations are state the
host owns rather than parallel executions of the node they share. You do not
see this from a model; it is why two approvers' outputs cannot cross.

[ADR-025](../../design/ADR-025-activity-iteration-loop-and-multi-instance.md)
§2.15/§2.15a and [ADR-020](../../design/ADR-020-human-interaction-execution-model.md)
§2.12 decide what the construct means.

## What the iterations produce

By default **the last write wins**. Each iteration runs in its own frame, but a
frame commits to the *enclosing scope* — isolation of an execution is not
invisibility of its writes. The consequence differs by shape, and both are
intended:

- **A sequential iteration is therefore a fold.** Pass *k* reads what pass
  *k-1* committed. That is the useful default: "keep a running total", "narrow
  a candidate set", "append to a report".
- **A parallel Multi-Instance is therefore order-dependent** for undeclared
  writes: which iteration's value survives depends on completion order, which
  the engine does not fix.

That second point is why the declared strategies exist. A model that needs
every iteration's result **says so**, and gets a deterministic one.

| Declare | Result |
|---|---|
| `WithOutputCollection(ref, item)` | Indexed by **ordinal** — slot *i* holds iteration *i*'s output, whatever order they completed in. This is BPMN's own `loopDataOutputRef` assembly. |
| `WithResultMap(name, item, key, …)` | Keyed by an expression evaluated in the **completing iteration's own frame**. |
| `WithResultReduce(name)` | Names the default, so a model can state the fold it relies on. Changes nothing. |

A Standard Loop gets `WithLoopResultArray(name, item)` and
`WithLoopResultMap(…)` — BPMN gives a loop no output aggregation at all, so
those are an engine extension.

### The map's key is the iteration's own

```go
byReviewer := goexpr.Must(nil,
    data.MustItemDefinition(values.NewVariable("")),
    func(ctx context.Context, src data.Source) (data.Value, error) {
        d, err := src.Find(ctx, "reviewer") // the element THIS iteration got
        if err != nil {
            return nil, err
        }

        return values.NewVariable(d.Value().Get(ctx)), nil
    })

mi, _ := activities.NewMultiInstance(
    activities.WithInputCollection("reviewers", "reviewer"),
    activities.WithResultMap("decisions", "decision", byReviewer))
```

It is evaluated **at that iteration's completion, in that iteration's frame**,
which is the point: the key can use something the iteration *produced*. The
assignee of a User Task is the motivating case — it is not known until the task
is claimed.

Two rules, and they differ deliberately:

- **An empty or missing key refuses.** There is no sensible slot for a result
  with no key, and silently dropping one iteration's output is the failure
  these strategies exist to make impossible.
- **A duplicate key overwrites by default**, consistent with the last-wins
  default rather than an exception to it. The loss stays *detectable*:
  `RUNTIME/ITERATIONS` publishes the total, so a map holding fewer entries than
  that says so. Pass `activities.ErrorOnKeyRewrite()` and a collision faults
  instead, naming both ordinals and the key — for a model where two iterations
  answering under one name is a modeling error, such as a fan-out over
  participants who must each answer once.

### One strategy per activity

The three are alternative *readings* of the same results, not stages of a
pipeline: an array and a map disagree about what a result is indexed by, and
reduce says there is nothing to assemble. Declaring a second is refused where
it is written.

### Nothing sees a half-assembled result

A declared array or map publishes to the enclosing scope **once, at activity
completion** — never incrementally. BPMN only *recommends* the output
collection be inaccessible until every iteration has finished; this engine
makes it a guarantee, and a declared result inherits it. The default has no
barrier by construction: it *is* the enclosing scope, written as the iterations
go.

Worked end-to-end: [`examples/multi-instance-human/`](../../../examples/multi-instance-human/).

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
**parallel** MI re-opens exactly its still-open iterations at their
recorded ordinals, with completed slots' outputs (and canceled slots'
holes) intact in the assembled output.


## See also

- Examples: [`multi-instance-sequential/`](../../../examples/multi-instance-sequential/) · [`multi-instance-parallel/`](../../../examples/multi-instance-parallel/) · [`multi-instance-behavior/`](../../../examples/multi-instance-behavior/)
- Related guides: [Standard Loop](standard-loop.md) · [Embedded Sub-Process](../subprocesses/embedded.md) · [Service Task](../tasks/service-task.md) · [Boundary events](../events/boundary.md)
- Design: [ADR-025 — Activity iteration: Loop & Multi-Instance](../../design/ADR-025-activity-iteration-loop-and-multi-instance.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/activities`
