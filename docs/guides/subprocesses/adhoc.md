---
title: Ad-Hoc Sub-Process
description: Work whose order is decided at runtime, by a Router.
---

# Ad-Hoc Sub-Process

An **Ad-Hoc Sub-Process** is an embedded Sub-Process whose inner activities
carry **no sequence flows**. The model declines to fix their order; what runs
next is decided while the case is running. Reach for it when the work is real
but the path is not knowable in advance — incident triage, case handling,
research, a doctor's rounds — and the alternative would be a gateway maze that
tries to enumerate every order in advance.

gobpm makes that decision a **Router**: a small piece of host code the engine
consults instead of following outgoing flows. This page is the developer
reference — the type, the Router contract, the shipped Routers, human
selection, and the runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Activity → Sub-Process → **Ad-Hoc** (§13.3.5) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/activities`; the routing contract in `github.com/dr-dobermann/gobpm/pkg/adhoc` |
| Type | `activities.SubProcess` (with `WithAdHoc(router)`) |
| Inherits | everything an embedded Sub-Process is — a nested scope, boundary events, data walk-up |
| Implements | `flow.Node`, `exec.NodeExecutor` (`Exec`), `flow.ActivityNode`; `IsAdHoc() bool` reports the flag and `AdHoc() AdHocSpec` exposes the configuration |
| The work | inner activities with no flows between them, run in the order a `adhoc.Router` answers |

Where it sits in the composition family: [Composition taxonomy](index.md). Like
the Transaction, it is not a distinct type — it is a `SubProcess` marked at
construction, so [Embedded Sub-Process](embedded.md) is its base in full.

## Constructor

Same constructor as any Sub-Process; the variant is an option:

```go
func NewSubProcess(name string, opts ...options.Option) (*SubProcess, error)
```

```go
triage, err := activities.NewSubProcess("triage",
    activities.WithAdHoc(myRouter{}))
```

`WithAdHoc` is **mutually exclusive** with `WithTransaction()` and
`WithTriggeredByEvent()`; combining them is a classified error at construction.

## Options

| Option | Effect |
|---|---|
| `WithAdHoc(r adhoc.Router)` | make the Sub-Process ad-hoc and attach its Router. A nil Router is rejected — routing is never implied. |
| `WithAdHocOrdering(o AdHocOrdering)` | `AdHocParallel` (default) allows several live activities at once; `AdHocSequential` permits one, and a multi-successor answer is then a classified error rather than a silent truncation. |
| `WithAdHocManualSelection()` | the Router's answer is **offered** rather than run: a host picks from the enabled set (see below). |
| `WithAdHocCancelRemaining(bool)` | when the completion condition fires with instances still live, `true` (the metamodel default) cancels them and `false` waits for them. |
| `WithAdHocCompletion(expr data.FormalExpression)` | the standard's `completionCondition`, evaluated after each inner completion; true ends the container. |

Each refining option requires `WithAdHoc` — using one alone names itself in the
error rather than being quietly ignored. The variant also accepts the shared
[activity options](../tasks/index.md#shared-activity-options).

## The Router

```go
type Router interface {
    Next(ctx context.Context, s adhoc.State) ([]string, error)
}
```

`Next` returns the ids of the inner activities that may run next. It is
consulted when the container's scope opens — that first answer is the
standard's *initially enabled* set — and again after **each** inner activity
settles, which is §13.3.5's "the enabled set is updated after each completion".

`State` is what the decision may rest on:

| Field | Meaning |
|---|---|
| `Activities []string` | the container's inner activity roster — a **set**, never an order. It is how you name an activity before anything has run. |
| `Completed map[string]int` | settled executions per activity. An activity that never ran is absent. |
| `Running map[string]int` | live instances per activity. Under parallel ordering one activity may hold several. |
| `Last string` | the activity whose completion triggered this call; empty at scope open. |
| `Data service.DataReader` | reads the ad-hoc scope, and the enclosing process's data by walk-up. A consistent snapshot for the whole decision. |
| `Eval adhoc.Evaluator` | evaluates a `FormalExpression` against that same scope, through the engine's language-routed expression seam. |

Two rules are worth internalising:

- **Everything is keyed by activity id.** Ids are unique; names are not. `Next`
  may answer with a name (it resolves either way), but a Router that reads
  `Completed`/`Running` should use ids, or the lookup silently misses. Give
  ad-hoc activities explicit, readable ids — `foundation.WithID("gather-logs")`
  — and both halves read the same.
- **An empty answer ends the asking track, not the container.** Siblings keep
  running and the Router is asked again as each settles. The container completes
  when its scope **drains** — there is no separate completion mechanism. This is
  also how an ad-hoc container joins without a join gateway: answer empty while
  `len(s.Running) > 0`, and answer the follow-up work when the last sibling
  settles.

A Router must be **prompt, read-only and free of blocking I/O**. The engine
evaluates it inline, where it evaluates its other conditions, so a Router that
blocks stalls its instance and one that calls back into its own instance
deadlocks. Waiting for a human is manual selection (below), never a slow Router;
a decision needing remote data reads it from the scope, where an earlier
activity put it.

## Shipped Routers

`github.com/dr-dobermann/gobpm/pkg/adhoc/routers` covers the common shapes, so
routine containers need no host code. Each is attached explicitly — the engine
applies none by default:

| Router | Behavior |
|---|---|
| `Standard()` | every activity enabled until it has run once; the container ends when all have. The conformance shape. Not for sequential ordering (it answers with the whole remaining set) — use `Sequence` there. |
| `Expression(expr)` | asks a BPMN expression, which must produce a `[]string` of ids or a single id string; empty ends the container. Any other result type is a classified error. |
| `Sequence(ids...)` | runs the named activities in order, one at a time, ending when the list is exhausted — the crystallized end state of a container whose order has hardened. |

```go
r, err := routers.Sequence("gather-logs", "notify-customer", "close-incident")
if err != nil {
    return err
}

triage, err := activities.NewSubProcess("triage", activities.WithAdHoc(r))
```

## Human selection

With `WithAdHocManualSelection()` the Router *proposes* and a host *disposes* —
§13.3.5's "one enabled Activity is selected for execution, typically by a Human
Performer". The container holds the answer as its enabled set and waits; the
wait costs no goroutine, since the scope's host track is already parked.

The control surface hangs off the instance handle, per container:

```go
ah, err := h.AdHoc(containerNodeID)   // an *AdHocHandle
enabled, err := ah.Enabled(ctx)       // what may be selected now
running, err := ah.Running(ctx)       // what is already in flight
err = ah.Activate(ctx, enabled[0])    // start one of them
```

Activating something not currently offered is a classified error, never a
silent no-op: a host acting on a stale view learns so. The whole offer is
consumed by an activation — the enabled set is re-derived from the Router once
that activity settles.

## Completion

Three things can end an ad-hoc container:

1. **The work runs out.** The Router answers empty and nothing is live, so the
   scope drains and the container completes — `stop_reason: router-empty`.
2. **The completion condition fires.** `WithAdHocCompletion(expr)` is evaluated
   after each inner completion; true ends the container, cancelling the live
   instances or waiting for them per `cancelRemainingInstances` —
   `stop_reason: completion-condition`. This is the *only* trigger that
   cancellation hangs off.
3. **Something cuts it short** — an interrupting boundary event, a scoped
   Terminate — `stop_reason: canceled`.

## Containment

§13.3.5 permits a richer inner set than gobpm admits today. The validated shape
is **leaf Tasks and plain embedded Sub-Processes**. Sequence flows between inner
activities, gateways, intermediate events, Start and End events, Event
Sub-Processes, Transactions and Call Activities are each rejected at
registration with a message naming what was found. A container is checked when
its process is registered, not when it runs.

## Observability

Routing decisions ride their own fact kind, `KindAdHoc`, echoed at `Info`:

| Phase | Carries |
|---|---|
| `Offered` | `candidates` — the comma-joined ids one Router answer produced. An **empty** value is meaningful: the container was consulted and had nothing to start. |
| `Activated` | `selected_by` — `router` or `host`. |
| `Completed` / `Canceled` | `stop_reason` — as listed under Completion. |

Every activation is preceded by an offer naming it, so a case's routing is
reconstructible from the stream alone. The container's scope lifecycle keeps
riding `KindScope`, so nothing is reported twice. See
[Observability](../operating/observability.md).

## Methods & runtime behavior

| Method | Behavior |
|---|---|
| `IsAdHoc() bool` | reports the variant. |
| `AdHoc() AdHocSpec` | the configuration — `Router()`, `Ordering()`, `IsManual()`, `CancelsRemaining()`, `CompletionCondition()`. A true nil for a plain Sub-Process. |
| `Add(e)` / `Nodes()` | the inner activities, as for any Sub-Process — but linked by nothing. |

At runtime the container opens an ordinary nested scope: inner activities run as
tracks inside it, read the scope's data with the usual walk-up, and the whole
container completes when the scope drains. Boundary events, compensation and the
Error scope chain behave exactly as they do on an embedded Sub-Process.

## Restarts

With a repository configured, an in-flight container is
checkpoint-faithful: the routing position — which activities have
completed and how often, a manual container's pending offer, a fired
completion condition — rides the checkpoint, and a recovered engine
restores the container **at that position**. The next Router decision
sees the true pre-crash progress, a restored offer is still visible in
`AdHocView` and consumable by `ActivateAdHoc`, and completed
activities never re-run. A checkpoint written by a pre-fidelity engine
(schema ≤ 4) with the container in flight refuses to restore loudly —
its routing state was never recorded. See
[Persistence](../operating/persistence.md).

## See also

- Family: [Composition taxonomy](index.md) · [Embedded Sub-Process](embedded.md) · [Transaction Sub-Process](transaction.md)
- Related: [Observability](../operating/observability.md) · [Expressions](../data/expressions.md)
- Example: [`examples/adhoc-subprocess/`](../../../examples/adhoc-subprocess/)
- Design: [ADR-035 — Ad-Hoc Sub-Process](../../design/ADR-035-adhoc-sub-process.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/adhoc` · `go doc github.com/dr-dobermann/gobpm/pkg/adhoc/routers`
