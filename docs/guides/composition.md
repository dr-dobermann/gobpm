# Composition — embedded sub-processes

How a gobpm process nests work into **scopes** (ADR-023, landed by
SRD-049): the embedded Sub-Process is an activity in its parent's graph AND
a container of its own inner graph, executed as a **nested scope inside the
same instance**.

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

The **Call Activity** — invoking a separately registered process as a child
instance — is the next slice of this workstream.
