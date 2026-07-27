---
title: Call Activity
description: Invoke a separately registered process as a child instance.
---

# Call Activity

A **Call Activity** invokes a *separately registered* process as its own child
instance — the reuse boundary. One callee, many callers, each call an isolated
run. Reach for it when a fragment (tax calculation, credit check, notification)
is worth registering once and reusing from several processes. Where an
[embedded Sub-Process](embedded.md) runs a nested scope *inside* the same
instance, a Call Activity launches a wholly separate instance of another
registered process; the two sides meet only through the declared **Input** and
**Output** parameters. This page is the developer reference — the type, its
constructor, its options, and its runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Activity → **Call Activity** (§10.4) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/activities` |
| Type | `activities.CallActivity` |
| Inherits | the `Activity` attributes and associations — I/O parameters, boundary events, loop characteristics |
| Implements | `flow.Node`, `exec.NodeExecutor` (`Exec`), `flow.ActivityNode` (`ActivityType`, `AddBoundaryEvent`) |
| The work | a separately registered process, named by `calledKey`, resolved at call time |

Where it sits in the composition family: [Sub-processes & reuse](index.md).

## Constructor

```go
func NewCallActivity(
    name, calledKey string,
    opts ...options.Option,
) (*CallActivity, error)
```

| Parameter | Meaning |
|---|---|
| `name` | the activity's diagram name (and default id source). |
| `calledKey` | the registry key of the process to invoke. The registry is **not** consulted here — resolution happens at call time (ADR-023 §2.7), so the callable may be registered later or re-versioned. |
| `opts` | zero or more options (below): activity options plus the Call-Activity-specific `WithCalledVersion`. |

It returns an error — never panics — on an invalid combination (e.g. a bad
option value).

## Options

Most Call Activities need only the two parameter declarations — the call
contract:

| Option | When you reach for it |
|---|---|
| `WithParameters(data.Input, params…)` | declare the inputs that cross **into** the child, matched by name. |
| `WithParameters(data.Output, params…)` | declare the outputs the child hands **back**, matched by name. |
| `WithCalledVersion(v)` | pin an exact registered version instead of latest-at-launch. |

The full set comes from two families — **activity options** (any activity) and
the **Call-Activity option** (`CallActivityOption`, version-pinning):

| Activity option | Effect |
|---|---|
| `WithParameters(d data.Direction, params ...*data.Parameter)` | declare the typed `data.Input` / `data.Output` call parameters. Under §10.4 direct mapping the parameter **name** is the whole contract — no data associations. |
| `WithoutParams()` | declare no parameters (a call that neither seeds nor reads back). |
| `WithLoop(lc)` / `WithMultyInstance()` | repeat the call — see [Standard Loop](../iteration/standard-loop.md), [Multi-Instance](../iteration/multi-instance.md). |
| `WithStartQuantity(n)` / `WithCompletionQuantity(n)` | BPMN token quantities (default 1). |

| Call-Activity option | Effect |
|---|---|
| `WithCalledVersion(v int)` | pin the call to an exact registered version of the callable (1-based, ADR-019). Without it the call binds **latest-at-launch** — the newest version registered at the moment the call executes. |

> Boundary events are attached with the method `AddBoundaryEvent`, not a
> constructor option — see [Boundary events](../events/boundary.md). A child
> `BpmnError` faults the caller *at the Call Activity node*, where an
> [Error boundary](../events/error.md) can catch it.

`WithCalledVersion` returns a `CallActivityOption`; `NewCallActivity` separates
these from the embedded activity's options and applies them to the
`CallActivity` itself. A bad version value is rejected with an error.

## Build it

The callee is an ordinary process, registered on its own by key. Nothing in it
knows it will be called:

```go
p, err := process.New("tax-calc", foundation.WithID(calcKey))
// … start → tax(reads "subtotal", writes "total") → end
```

The caller adds a Call Activity naming the callee's key, plus the **Input** and
**Output** parameters. The parameter *name* is what crosses the boundary:

```go
call, err := activities.NewCallActivity("charge", calcKey,
    activities.WithParameters(data.Input, param("subtotal")),
    activities.WithParameters(data.Output, param("total")))
```

Register both processes with the engine, then start the caller:

```go
engine.RegisterProcess(callee)   // "tax-calc" — the reusable child
engine.RegisterProcess(caller)   // "checkout" — invokes it
h, _ := engine.StartLatest(caller.ID())
state, _ := h.WaitCompletion(ctx)
```

## Run it

Running `examples/call-activity/` — the caller seeds `subtotal=100`, the child
computes `total=120` and hands it back:

```
  ▶ call charge: Started (tax-calc v.1 → instance 4672822989981304722)
    (child) subtotal=100 → total=120
  ✓ caller sees total=120
  ▶ call charge: Completed (tax-calc v.1 → instance 4672822989981304722)
  ✓ completed (Completed)
```

## Methods & runtime behavior

The engine drives the activity through these — you rarely call them directly:

| Method | Role |
|---|---|
| `Exec(ctx, re) ([]*flow.SequenceFlow, error)` | park the caller's token, launch the child through the registry, then resume onto the outgoing flows when it completes. |
| `CalledKey() string` / `CalledVersion() int` | the resolved call target — the key and pinned version (0 when latest-at-launch). |
| `CallInputs() []string` / `CallOutputs() []string` | the declared parameter names — the call contract. |
| `AddBoundaryEvent(be)` / `BoundaryEvents()` | attach / inspect boundary events. |
| `ActivityType()` / `NodeType()` | introspection. |

Behavior worth knowing — what happens when the caller's token reaches the node:

- the token **parks** and the engine launches the callee through its registry —
  **latest-at-launch** by default, or the version pinned with
  `WithCalledVersion`;
- the declared **Input** parameters are resolved by name at the caller's scope
  and **cloned across the boundary** — the child runs on an isolated data plane,
  with no walk-up to the caller. This is the isolation contract, unlike the
  embedded Sub-Process. The child cannot see the caller's other properties;
- when the child completes, its declared **Output** parameters are read by name
  from the child's root and **committed back** into the caller's scope, and the
  caller resumes onto its outgoing flows;
- a child `BpmnError` faults the caller *at the Call Activity node* (the
  scope-chain error rules); uncaught, the instance faults. The child
  **terminates with the caller** — the cancel cascade fires when the caller
  track ends, its scope is canceled, or the instance terminates;
- `Call` facts carry `Started` / `Completed` / `Failed` / `Terminated` phases
  with the called key, the *resolved* version, and the child instance id; every
  fact the child emits carries `parent_instance_id` and
  `call_activity_node_id`, stitching the trace across the boundary.

## See also

- Examples: `examples/call-activity/`
- Related guides: [Embedded Sub-Process](embedded.md) · [Transaction Sub-Process](transaction.md) · [Registering & versioning](../operating/registering-and-versioning.md) · [Error events](../events/error.md)
- Design: [ADR-023 — Sub-Process & Call Activity](../../design/ADR-023-sub-process-and-call-activity.md) · [ADR-019 — Definition versioning](../../design/ADR-019-definition-versioning.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/activities`
