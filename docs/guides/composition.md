# Composition — sub-processes & call activities

How a gobpm process composes work (ADR-023): the **embedded Sub-Process**
(landed by SRD-049) is an activity in its parent's graph AND a container of
its own inner graph, executed as a **nested scope inside the same
instance**; the **Call Activity** (SRD-050) invokes a separately registered
process as its own **child instance** — the reuse boundary.

## Building one

```go
fulfil, _ := activities.NewSubProcess("fulfil")

fStart, _ := events.NewStartEvent("f-start")   // exactly one None start…
// …build inner nodes, fulfil.Add(...) each, flow.Link(...) inside

p.Add(fulfil)                                   // an activity in the parent
flow.Link(accept, fulfil)                       // enters like any activity
flow.Link(fulfil, notify)                       // continues when it drains
```

Validation (at registration) enforces the BPMN §13.3.4 shapes: **exactly
one None Start Event**, XOR **no start event** — then every flow-less inner
activity/gateway gets a token. Triggered starts, mixed shapes, multiple
starts, an empty body, and flows crossing the boundary are rejected.

## The scope

Entering the composite **opens a child scope**; the host token parks; the
scope **completes when no tokens remain inside** (§13.3.4) and the host
resumes onto its outgoing flows (conditional/default rules included).
Nesting is unbounded — scopes form a tree (`/proc/sp-a/sp-b`).

**Data** follows §10.5.7: inner nodes read the parent's data through the
walk-up; their own committed locals live in the child scope and are
**disposed at close**. A conditional event inside a sub-process evaluates
at its own scope — parent data included.

## Interruption — the scope dies as a unit

| Construct | Effect |
|---|---|
| **Boundary event on the composite** (interrupting) | cancels every inner track, closes the scope, routes the exception flow. Non-interrupting forks in parallel as usual. |
| **Terminate End Event inside** (§13.5.6) | discards only ITS scope's tokens; the composite completes and the **parent continues**. A top-level Terminate still ends the instance. |
| **Error** (a failing inner activity or an Error End Event inside) | walks the **scope chain** to the innermost enclosing catcher: the failing activity's own Error boundary, then each enclosing composite's; a catch cancels that scope and routes its exception flow; no match anywhere faults the instance. |

## Observability

Scope lifecycle is first-class: `Scope` facts with `Opened` / `Completed` /
`Terminated` / `Canceled` phases and the scope path in the details; the
composite node also emits the usual `NodeProgress` facts.

## Worked example

[`examples/embedded-subprocess/`](../../examples/embedded-subprocess/) —
an order flow with the fulfillment fragment as a sub-process: walk-up
reads, scoped locals, the drain-resume ordering, and the scope facts.

# Event Sub-Process — a scope-armed handler

An **Event Sub-Process** is a `SubProcess` marked `triggeredByEvent` that
lives *inside* another scope (ADR-023 v.2 §2.10, SRD-052). It is not entered
by a token — it is **armed** while its enclosing scope is open and fires when
its single triggered start catches an event: the boundary-event pattern lifted
from an activity's window to a **scope's** window.

```go
onTimeout, _ := activities.NewSubProcess("payment-timeout",
    activities.WithTriggeredByEvent())
start, _ := events.NewStartEvent("timeout-fired",
    events.WithTimerTrigger(timeoutTimer())) // interrupting is the default
// onTimeout.Add(start, …); the handler lives among its scope's inner nodes
```

The triggered start carries the handler's trigger: a **Message**, **Timer**,
**Signal**, or **Conditional** start (armed as the enclosing scope's own
subscription), or an **Error** start (caught on the scope chain at the throw
site, alongside the composite's Error boundary — innermost catcher wins).

An **interrupting** handler (the default — BPMN §13.5.4; `WithNonInterrupting`
flips it) fires a **cancel-and-run**:

- it **cancels** the enclosing scope's sibling tracks, but the scope's data
  plane **stays open** — so the handler runs in the parent's data context;
- it **runs its own flow** in a fresh child scope seeded from the triggered
  start (treated as fired, its payload bound);
- reaching its End without re-throwing, it **absorbs** the event: the scope
  completes and the parent resumes on its **normal** flow. A handler that
  re-throws re-enters the throw path so a boundary fires after.

A **non-interrupting** handler (`WithNonInterrupting()`, any trigger except
Error — Error is interrupting-only) instead **forks**: on each fire it spawns a
handler instance in its own fresh child scope **without** cancelling the scope,
and the watch stays armed, so it fires **again** on the next trigger — unlimited
concurrent instances. The enclosing sub-process completes only once its own work
**and every live handler instance** have drained.

A scope allows **one** interrupting fire: the first — an event sub-process
**or** a boundary event on the composite — spends the budget and suppresses
the rest, so the two constructs cooperate rather than double-fire.
Non-interrupting fires don't touch that budget.

Handler lifecycle is observable through `Boundary`-kind facts carrying a scope
path (`Armed` / `Fired` / `Disarmed`), next to the `Scope` cancel/complete
facts.

[`examples/event-subprocess/`](../../examples/event-subprocess/) — a checkout
whose payment wait is interrupted by a Timer handler that releases the hold
and lets the parent continue.

# Call Activity — a child instance (the reuse boundary)

Where the embedded Sub-Process runs *inside* the instance, a **Call
Activity** invokes a **separately registered process as its own child
instance** (ADR-023 §2.7, SRD-050). It is the reuse boundary: one callee,
many callers, each call an isolated run.

```go
call, _ := activities.NewCallActivity("charge", "tax-calc",
    activities.WithParameters(data.Input, subtotalParam),
    activities.WithParameters(data.Output, totalParam))
// pin an exact version instead of latest-at-launch:
//   activities.NewCallActivity("charge", "tax-calc",
//       activities.WithCalledVersion(2), …)
```

When the caller's token reaches it:

- the token **parks** and the loop launches the callee through the engine's
  registry — **latest-at-launch** by default (ADR-019), or the version
  pinned with `WithCalledVersion`;
- the declared **Input** parameters are resolved by name at the caller's
  scope and **cloned across the boundary** — the child runs on an **isolated
  data plane**, with no walk-up to the caller (the isolation contract,
  unlike the embedded Sub-Process);
- when the child completes, its declared **Output** parameters are read by
  name from the child's root and **committed back** into the caller's scope,
  and the caller resumes onto its outgoing flows.

## Reuse boundary vs nested scope

| | Embedded Sub-Process | Call Activity |
|---|---|---|
| Runtime | a **nested scope** in the same instance | a **separate child instance** |
| Data | walk-up to the parent (§10.5.7) | isolated; only declared I/O crosses (cloned) |
| Definition | inline (`Add` inner nodes) | a separately registered process (by key) |
| Versioning | — | latest-at-launch, or `WithCalledVersion` |

## Interruption & errors

A child `BpmnError` faults the caller **at the Call Activity node**: an Error
boundary on it catches per the same scope-chain rules above (an untyped
child termination is a technical fault — uncaught, the instance faults). The
child **terminates with the caller** — the cancel cascade fires when the
caller track ends, its scope is canceled, or the instance terminates.

## Call observability

`Call` facts with `Started` / `Completed` / `Failed` / `Terminated` phases
carry the called key, the **resolved** version (the latest-at-launch audit
point), and the child instance id; every fact the **child** emits carries
`parent_instance_id` + `call_activity_node_id`, stitching the trace across
the boundary.

## Call worked example

[`examples/call-activity/`](../../examples/call-activity/) — a `checkout`
process reusing a `tax-calc` child: the input crosses in cloned, the child
computes, the output crosses back, and the Call facts trace both sides.
