---
title: Timer events
description: Wait for a duration or an instant.
---

# Timer events

A **timer event** ties process flow to the clock: fire at a specific instant,
after a delay, or on a repeating cycle — no scheduling loop of your own. The
timing lives in a **`TimerEventDefinition`**, which you attach to a catching
event: a **start** event (the engine instantiates the process when the timer
fires), an **intermediate catch** event (the track parks until it fires), or a
**boundary** event (an armed timeout on an activity). This page is the developer
reference — the definition's constructor, the three mutually-exclusive timing
expressions, the trigger option that attaches it, and its runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Event → Definition → **Timer** (`§10.5.3`) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/events` |
| Type | `events.TimerEventDefinition` |
| Embeds | the shared event `definition` (id, `GetItemsList`, `CloneForInstance`) |
| Implements | `flow.EventDefinition` — `Type() flow.EventTrigger` returns `flow.TriggerTimer` |
| Attaches to | a Start / Intermediate-catch / Boundary event via `WithTimerTrigger(ted)` |
| The timing | one of three `data.FormalExpression`s — `timeDate`, `timeCycle`, `timeDuration` |

Where it sits in the event family: [Events taxonomy](index.md).

## Constructor

```go
func NewTimerEventDefinition(
    tDate, tCycle, tDuration data.FormalExpression,
    baseOpts ...options.Option,
) (*TimerEventDefinition, error)
```

| Parameter | Meaning |
|---|---|
| `tDate` | fire once at an instant — expression result type **`Time`**. |
| `tCycle` | repeat count — expression result type **`int`** (paired with `tDuration`). |
| `tDuration` | a delay/interval — expression result type **`Duration`**. |
| `baseOpts` | base-element options (e.g. `foundation.WithID`). |

**Three forms are accepted**, and nothing else:

| Form | Meaning |
|---|---|
| `tDate` alone | an **absolute** deadline — fire once, at that instant. |
| `tDuration` alone | a **relative** deadline — fire once, that long after the timer arms. This is BPMN's `timeDuration` (§10.5.5, Table 10.101): *"wait five minutes, then fire"*. |
| `tCycle` **with** `tDuration` | a **recurrence** — fire `tCycle` times, `tDuration` apart. |

The recurrence is where gobpm departs from the XML notation. BPMN packs both
numbers into one ISO 8601 string on `timeCycle` (`R3/PT10H`); the engine carries
them as two typed expressions instead of a parsed string. That is why
`tDuration` is required alongside `tCycle`, and why `tCycle` alone is refused —
a repetition count with no interval has nothing to schedule. Both spellings
denote the same schedule.

Three errors can come back, each naming the rule it broke:

| Condition | Error |
|---|---|
| all three nil | `"NewTimerEventDefinition: a Timer needs timeDate, timeDuration, or timeCycle with timeDuration"` (`InvalidParameter`) |
| `tDate` set alongside `tCycle` or `tDuration` | `"NewTimerEventDefinition: timeDate is mutually exclusive with timeCycle and timeDuration (BPMN Table 10.101)"` (`InvalidParameter`) |
| `tCycle` without `tDuration` | `"NewTimerEventDefinition: timeCycle needs timeDuration as its interval — a recurrence is carried as (count, interval)"` (`InvalidParameter`) |
| an expression's `ResultType()` ≠ its slot's type | `"expression result isn't desired type"` (`InvalidObject`, with `expected_type`/`expr_type` details) |

It returns an error — never panics. `MustTimerEventDefinition(tDate, tCycle,
tDuration, …)` is the panic-on-error twin for static wiring.

## Options

A timer definition is not itself option-configured for timing — the timing is
its three positional expressions. The only option you routinely pass is a
base-element one:

| Option | When you reach for it |
|---|---|
| `foundation.WithID(id)` | pin the definition's id (else it is generated). |

The definition is **attached** to an event with a trigger option — this is
where "timer" meets a real node:

| Trigger option | Effect |
|---|---|
| `events.WithTimerTrigger(ted *TimerEventDefinition)` | add the timer definition to a Start / Intermediate-catch / Boundary event's config. |

`WithTimerTrigger` is an `events.EventOption`. The receiving constructor
allow-lists `flow.TriggerTimer`, so it is accepted by `NewStartEvent`,
`NewIntermediateCatchEvent`, and `NewBoundaryEvent`, and rejected elsewhere
(e.g. an end event) at build time.

> A **boundary** timer is attached through `NewBoundaryEvent(name, host, ted,
> …)` rather than a start-event option — see [Boundary events](boundary.md).

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/events`.

## Build the timing expression

A timer's `when` is a `data.FormalExpression` that yields the slot's type. Wrap
a Go closure with `goexpr.Must`; for a `timeDate` it must return a `time.Time`:

```go
timeExpr := goexpr.Must(
    nil,
    data.MustItemDefinition(values.NewVariable(time.Now().Add(3*time.Second))),
    func(ctx context.Context, ds data.Source) (data.Value, error) {
        return values.NewVariable(time.Now().Add(3 * time.Second)), nil
    },
    foundation.WithID("timer-3s"),
)
```

Because the `when` is an expression evaluated by the engine (not a captured
constant), each armed instance recomputes it — the closure above returns a fresh
`time.Now().Add(3 * time.Second)` on evaluation.

## Timer start event

Feed the expression into a `TimerEventDefinition` as its `timeDate`, then attach
it to a start event with `WithTimerTrigger`. From
[`examples/simple-timer/`](../../../examples/simple-timer/):

```go
timerDef, _ := events.NewTimerEventDefinition(timeExpr, nil, nil)

timerStart, _ := events.NewStartEvent("timer-start",
    events.WithTimerTrigger(timerDef))

endEvent, _ := events.NewEndEvent("end")

proc.Add(timerStart)
proc.Add(endEvent)
flow.Link(timerStart, endEvent)
```

Running `examples/simple-timer/` — register, `Run` the engine, and the instance
auto-starts when the timer fires (no `StartLatest` call):

```
Timer process started. Will trigger in 3 seconds...
Timer should have fired by now!
```

> A timer **start** event needs no explicit `StartLatest` — the engine
> instantiates the process itself when the timer fires. You only
> `RegisterProcess` and `Run` the engine, then wait.

## Timer into real work

Point the timer start at a task instead of straight to end — the same
definition, a longer flow. From
[`examples/timer-event/`](../../../examples/timer-event/):

```go
timerEvent, _ := events.NewStartEvent("timer-start",
    events.WithTimerTrigger(timerDef))
serviceTask, _ := activities.NewServiceTask("handle-timeout", op,
    activities.WithoutParams())

flow.Link(timerEvent, serviceTask)
flow.Link(serviceTask, endEvent)
```

Other flavours reuse the same constructor:

- **Duration** — `NewTimerEventDefinition(nil, nil, durationExpr)`, where
  `durationExpr` has result type `Duration`, for a "fire N after arming" delay.
- **Recurring** — `NewTimerEventDefinition(nil, cycleExpr, durationExpr)`, an
  `int` count paired with the interval `Duration`.

## Methods & runtime behavior

The engine drives the definition through these — you rarely call them directly:

| Method | Role |
|---|---|
| `Type() flow.EventTrigger` | reports `flow.TriggerTimer`; how allow-lists classify it. |
| `Time()` / `Cycle()` / `Duration()` | read back the `timeDate` / `timeCycle` / `timeDuration` expression. |
| `CloneForInstance() flow.EventDefinition` | per-instance copy with a **fresh id**, sharing the (immutable) timer expressions by reference. |
| `GetItemsList()` | the item definitions the timing expressions carry. |

Behavior worth knowing: the definition holds *when*, and the engine arms it —
at registration for a start event, on entry for an intermediate/boundary event.
`CloneForInstance` gives each instance its own registration identity so
concurrent instances waiting on the same timer register **distinct** EventHub
waiters — without it one timer occurrence would resume them all (the timer
analog of the message per-instance clone). A timer carries no payload, so the
clone shares the expressions and only refreshes the id.

With a repository configured, a **one-shot** timer more than an hour out does
not keep a waiter at all: the engine's timer service holds its absolute deadline
and the instance **dehydrates**, so a thousand instances waiting on a two-day
timer cost zero goroutines. Shorter one-shots and any **repeating** timer keep
their in-memory waiter and stay resident. See
[Persistence & recovery](../operating/persistence.md).

## See also

- Examples: [`examples/simple-timer/`](../../../examples/simple-timer/) (start → end) · [`examples/timer-event/`](../../../examples/timer-event/) (start → service task → end) · [`examples/usertask-sla/`](../../../examples/usertask-sla/) (**`timeDuration` alone** — three non-interrupting boundary timers marking 50% / 90% / 100% of a UserTask's SLA)
- Related guides: [Start & End](start-and-end.md) · [Boundary events](boundary.md) · [How events are processed](../concepts/event-processing.md) · [Expressions](../data/expressions.md)
- Design: [ADR-015 — Event-triggered instantiation](../../design/ADR-015-event-triggered-instantiation.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/events`
