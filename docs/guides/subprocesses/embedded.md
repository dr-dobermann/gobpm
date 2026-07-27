---
title: Embedded Sub-Process
description: A nested scope in the same instance.
---

# Embedded Sub-Process

An embedded Sub-Process is two things at once: an ordinary activity in its
parent's graph, and a container of its own inner graph. A token enters it like
any node, but entering **opens a child scope** that runs the inner flow while
the host token parks; the host resumes only when that scope drains. Reach for it
to group a stretch of work — a fulfillment fragment, a validation block — into
one node with its own data scope, without spinning up a separate process. This
page is the developer reference — the type, its constructor, its options, how you
fill and wire it, and its runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Activity → **Sub-Process** (embedded) (§10.2.5, §13.3.4) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/activities` |
| Type | `activities.SubProcess` |
| Embeds | `flow.ElementsContainer` — the id-keyed graph core (nodes + sequence flows) it shares with `process.Process` |
| Inherits | the `Activity` attributes — I/O sets, boundary events, loop characteristics, compensation, default flow |
| Implements | `flow.Node`, `flow.ActivityNode` (`ActivityType`, `AddBoundaryEvent`), `flow.Container` (`Add`/`Remove`), `exec.NodeExecutor` (`Exec`), and `Validate` (shape rules) |
| The work | the inner graph you build with `Add` + `flow.Link` |

Where it sits in the composition family: [Sub-processes & reuse](index.md).

## Constructor

```go
func NewSubProcess(
    name string,
    opts ...options.Option,
) (*SubProcess, error)
```

| Parameter | Meaning |
|---|---|
| `name` | the sub-process's diagram name (and default id source). |
| `opts` | zero or more options (below) — sub-process markers plus the shared activity options. |

`NewSubProcess` creates an **empty** container. You add inner elements
afterwards with `Add`; the shape rules are **not** checked at construction —
a container is legitimately built element by element — but at process validation
(registration) via `Validate`. The constructor returns an error (never panics)
on a bad option combination.

> `WithTriggeredByEvent()` does **not** build an embedded Sub-Process — it makes
> an [Event Sub-Process](../events/event-subprocess.md) (a scope-armed handler).
> Reach for the plain constructor for the embedded case.

## Options

Most embedded Sub-Processes need **no options at all** — you build them empty
and fill them with `Add`. The two sub-process markers switch the element to a
different BPMN kind instead:

| Option | When you reach for it |
|---|---|
| *(none)* | the ordinary embedded Sub-Process — a nested scope you fill with `Add`. |
| `WithTransaction()` | make it a [Transaction Sub-Process](transaction.md) — Cancel End / boundary enabled, ACID-like abort. |
| `WithTriggeredByEvent()` | make it an [Event Sub-Process](../events/event-subprocess.md) — a handler entered by its triggered start, not by a flow. |

The options come from two typed families — **sub-process options**
(`SubProcessOption`, applied to the container itself) and the shared **activity
options** (any activity):

| Sub-process option | Effect |
|---|---|
| `WithTransaction()` | mark it a Transaction Sub-Process (BPMN §10.7); permits Cancel (End + boundary) and names the scope a Cancel aborts. Mutually exclusive with `WithTriggeredByEvent`. |
| `WithTriggeredByEvent()` | mark it an Event Sub-Process (BPMN §13.5.4); entered only by its single triggered Start Event, not by a sequence flow. |

| Activity option | Effect |
|---|---|
| `WithParameters(dir data.Direction, params ...*data.Parameter)` | declare typed inputs/outputs on the sub-process activity. |
| `WithoutParams()` | declare no parameters. |
| `WithCompensation()` | mark the sub-process a compensation handler (armed, off the normal flow). |
| `WithLoop(lc)` / `WithMultyInstance()` | repeat the whole scope — see [Standard Loop](../iteration/standard-loop.md), [Multi-Instance](../iteration/multi-instance.md). |
| `WithStartQuantity(n)` / `WithCompletionQuantity(n)` | BPMN token quantities (default 1). |

> Boundary events are attached with the method `AddBoundaryEvent`, not a
> constructor option — see [Boundary events](../events/boundary.md).

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/activities`.

## The container contract

A `SubProcess` is a `flow.Container`: you populate it exactly like a
`process.Process` — `Add` each inner element, then `flow.Link` them **inside**
the container. The same-container rule confines a nested graph to its owner, so
a sequence flow may never cross the boundary.

```go
func (sp *SubProcess) Add(e flow.Element) error       // node, flow, or Data Object into the inner graph
func (sp *SubProcess) Remove(e flow.Element) error    // remove an inner element
func (sp *SubProcess) Validate() error                // the §13.3.4 shape rules, run at registration
```

`Validate` (invoked by `Process.Validate`, recursing into nested sub-processes)
enforces the two normative §13.3.4 shapes — **exactly one None Start Event**,
XOR **no start event with at least one flow-less inner activity/gateway**
(seeded directly). A triggered start, multiple starts, a mixed shape, an empty
container, an inner flow with an endpoint outside the container, or a boundary
event whose host is outside — all classified errors.

## Build it

Create the sub-process, build its inner nodes, and `Add` each one **into** the
sub-process (not the parent). The inner graph needs its own None Start Event
(from [`examples/embedded-subprocess/`](../../../examples/embedded-subprocess/)):

```go
fulfil, _ := activities.NewSubProcess("fulfil")

fStart, _ := events.NewStartEvent("f-start")   // exactly one None start
pick, _ := step("pick", "picked", 1)
pack, _ := step("pack", "", 0)
fEnd, _ := events.NewEndEvent("f-end")

for _, e := range []flow.Element{fStart, pick, pack, fEnd} {
    fulfil.Add(e)
}
```

The sub-process itself is added to the **parent** and linked like any activity —
one flow enters it, one continues when it drains. Inner links stay inside the
fragment (`fStart → pick → pack → fEnd`):

```go
proc.Add(fulfil)             // an activity in the parent's graph
flow.Link(accept, fulfil)    // enters like any activity
flow.Link(fulfil, notify)    // continues when the scope drains
```

Inner tasks reach the parent's `order-id` property by name — no extra wiring;
the container walk-up (§10.5.7) resolves it:

```go
op, _ := gooper.New(name,
    func(_ context.Context, ds service.DataReader,
        _ *data.ItemDefinition) (*data.ItemDefinition, error) {
        d, _ := ds.GetData("order-id")   // the parent's property, via walk-up
        fmt.Printf("  %s (sees order-id=%v)\n",
            name, d.Value().Get(context.Background()))
        return nil, nil
    })
```

## Run it

```bash
cd examples/embedded-subprocess && go run .
```

The parent runs `accept`; the fragment opens its scope and runs `pick`/`pack`;
the scope completes and the parent resumes onto `notify` — `notify` therefore
always prints after both inner tasks:

```
  accept (sees order-id=4711)
  ▶ scope fulfil: Opened (/embedded-subprocess/sp-2155113504526615022)
  pick (sees order-id=4711)
  pack (sees order-id=4711)
  ▶ scope fulfil: Completed (/embedded-subprocess/sp-2155113504526615022)
  notify (sees order-id=4711)
  ✓ completed (Completed)
```

The scope lifecycle is observable: subscribe an observer and watch for
`KindScope` facts, whose `Phase` moves `Opened → Completed` and whose details
carry the scope path (`AttrScopePath`):

```go
func (p *scopePrinter) OnFact(f observability.Fact) {
    if f.Kind != observability.KindScope {
        return
    }
    fmt.Printf("  ▶ scope %s: %s (%s)\n",
        f.NodeName, f.Phase, f.Details[observability.AttrScopePath])
}
```

## Methods & runtime behavior

The engine drives the sub-process through these — you rarely call them directly:

| Method | Role |
|---|---|
| `Add(e)` / `Remove(e)` / `Validate()` | populate and shape-check the inner graph (build time / registration). |
| `Exec(ctx, re) ([]*flow.SequenceFlow, error)` | runs **after** the scope drained and the host resumed — selects the outgoing flows (conditional / default rules included). |
| `AddBoundaryEvent(be)` / `BoundaryEvents()` | attach / inspect interrupting boundary handlers on the scope. |
| `ActivityType()` / `IsEventSubProcess()` / `IsTransaction()` | introspection — which sub-process flavour this is. |
| `Properties()` / `DataObjects()` | the sub-process-level named data containers. |

Behavior worth knowing:

- **Enter and park.** When the host token reaches the sub-process, a child scope
  opens (path `/proc/sp-…`) and the inner flow seeds from its None Start Event
  (or, in the no-start shape, every flow-less inner node is seeded directly).
  The host token **parks** on the node; `Exec` runs only after drain.
- **Drain, then resume.** The scope completes when **no tokens remain inside**;
  only then does the host resume onto its outgoing flow.
- **Data walk-up.** Inner nodes read the parent's data through the container
  walk-up (§10.5.7). A task's own commit lands in the **child** scope and is
  **disposed at close** — the parent never sees it.
- **Interruption is scope-wide.** The scope dies as a unit: an interrupting
  boundary event on the sub-process cancels every inner track and routes its
  exception flow; a Terminate End Event inside discards only *this* scope's
  tokens and lets the parent continue; an Error escaping an inner activity walks
  the scope chain to the innermost enclosing catcher.
- **Nesting is a tree.** Scopes nest without bound — a sub-process inside a
  sub-process just deepens the path (`/proc/sp-a/sp-b`).

When the fragment is a whole reusable process with its own isolated data, use a
[Call Activity](call-activity.md) instead of an embedded scope.

## See also

- Examples: [`examples/embedded-subprocess/`](../../../examples/embedded-subprocess/)
- Related guides: [Call Activity](call-activity.md) · [Transaction Sub-Process](transaction.md) · [Event sub-processes](../events/event-subprocess.md) · [Boundary events](../events/boundary.md) · [Process, instance, track, token](../concepts/execution-model.md)
- Design: [ADR-023 — Sub-Process & Call Activity](../../design/ADR-023-sub-process-and-call-activity.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/activities`
