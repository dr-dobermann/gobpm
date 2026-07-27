---
title: Script Task
description: An inline expression/script step.
---

# Script Task

A Script Task runs a snippet of script — a `.lua` file, an expression, a small
DSL — that the engine evaluates in place. Reach for it when a step is pure data
logic (classify, compute, decide) that you'd rather keep as editable script than
compile into Go. The task carries a **`scriptFormat`** MIME hint and a **script
body**; at activation the engine routes the body to the registered Script Engine
that claims that format, runs it, and commits the script's named results as
process data for downstream nodes to read. This page is the developer reference —
the type, its constructor, the `script.Engine` seam it dispatches to, and its
runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Activity → Task → **Script Task** |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/activities` |
| Type | `activities.ScriptTask` |
| Inherits | the `Activity` attributes and associations — I/O sets, boundary events, loop characteristics, compensation, start/completion quantities |
| Implements | `flow.Node`, `exec.NodeExecutor` (`Exec`), `exec.NodeDataConsumer`/`Producer` (`LoadData`/`UploadData`), `flow.ActivityNode` (`ActivityType`, `AddBoundaryEvent`) |
| The work | a script body in a `scriptFormat`, run by the registered `script.Engine` |

Where it sits in the activity family: [Activities taxonomy](index.md).

## Constructor

```go
func NewScriptTask(
    name, format, body string,
    opts ...options.Option,
) (*ScriptTask, error)
```

| Parameter | Meaning |
|---|---|
| `name` | the task's diagram name (and default id source). |
| `format` | the `scriptFormat` MIME hint (e.g. `text/x-lua`) — routes the body to the engine claiming it. |
| `body` | the script source — an inline literal or an embedded file (`//go:embed`). |
| `opts` | zero or more options (below). |

It returns an error — never panics. **All three of `name`, `format`, `body` are
required.** The BPMN metamodel carries `scriptFormat` and `script` as `0..1` for
interchange, but a scriptless Script Task in a programmatic model is a bug, so an
empty `format` or `body` fails fast at construction.

## Options

Most Script Tasks need no options at all — the script reads process data lazily
by name and returns its outputs. The declared-I/O and repetition options come
from the shared **activity option** family (any activity):

| Activity option | Effect |
|---|---|
| `WithParameters(d data.Direction, params ...*data.Parameter)` | declare typed inputs/outputs (default: none — the script reads/writes data by name). |
| `WithoutParams()` | declare no parameters explicitly. |
| `WithCompensation()` | mark the task a compensation handler (armed, off the normal flow). |
| `WithLoop(lc)` / `WithMultyInstance()` | repeat the activity — see [Standard Loop](../iteration/standard-loop.md), [Multi-Instance](../iteration/multi-instance.md). |
| `WithStartQuantity(n)` / `WithCompletionQuantity(n)` | BPMN token quantities (default 1). |

> Boundary events are attached with the method `AddBoundaryEvent`, not a
> constructor option — see [Boundary events](../events/boundary.md).

There are no Script-Task-specific options — the engine that runs the body is
registered on the `Thresher`, not on the task. For the complete, always-current
signatures run `go doc github.com/dr-dobermann/gobpm/pkg/model/activities`.

## The Script Engine seam

The task does not interpret the script itself. At `Exec`, it routes the body by
its `scriptFormat` to a registered `script.Engine`. The engine is the seam you
plug an interpreter into (`pkg/script`, ADR-031):

```go
type Engine interface {
    Type() string                 // engine kind, "##"-convention (e.g. "##Lua")
    Formats() []string            // the MIME hints this engine claims
    Execute(
        ctx context.Context,
        format, script string,
        r service.DataReader,     // read-only process-data surface
    ) (Outputs, error)
}

type Outputs map[string]data.Value // named results, each committed as a Ready datum
```

You register engines on the engine with the **repeatable** `thresher.WithScriptEngine(e)`
option. Each call adds another engine; their format claims fold into an immutable
routing `Registry` (itself an `Engine`) at `New`, where a duplicate claim fails
construction loud. With no engine registered the default is the empty `"##None"`
registry — a Script Task then fails with a wire-an-adapter error, not silently.

The core stays stdlib-light: interpreters live in adapter modules. The batteries
one is `adapters/lua` (`lua.New()`), claiming `text/x-lua`. Building your own
engine is [Custom script engine](../extending/script-engine.md).

## Build it

Register a Script Engine on the `Thresher` — the option is repeatable, so more
interpreters can coexist, each owning its formats:

```go
engine, err := thresher.New(
    fmt.Sprintf("script-task-%d", total),
    thresher.WithScriptEngine(lua.New()))
```

Construct the task with a name, its `scriptFormat`, and the body. Here the body
is an embedded `.lua` file, editable without recompiling the model:

```go
//go:embed order.lua
var orderLua string

classify, err := activities.NewScriptTask("classify", "text/x-lua", orderLua)
```

The script reads process data lazily by name and returns its outputs as a table
(`order.lua`); the downstream `report` task reads those committed outputs by name
exactly as it would any process property:

```go
pct, _ := r.GetData("discount_pct")
lane, _ := r.GetData("lane")
```

## Run it

Running `examples/script-task/` — each order runs its own instance; the Lua
`print` line and the matching `report` line show the classification:

```
order: tier="vip" total=500
  [script] tier=vip total=500 -> 25%
  [report] lane=wholesale discount=25%

order: tier="retail" total=150
  [script] tier=retail total=150 -> 15%
  [report] lane=wholesale discount=15%

order: tier="" total=40
  [script] tier=retail total=40 -> 5%
  [report] lane=retail discount=5%

✓ script-task completed: three orders classified by the sandboxed Lua script (25/15/5%)
```

The third order declares no `tier` datum at all — the script's `has("tier")`
probe falls through to the retail default, proving lazy fail-loud reads and
explicit optional-data probing.

## Methods & runtime behavior

The engine drives the task through these — you rarely call them directly:

| Method | Role |
|---|---|
| `Exec(ctx, re) ([]*flow.SequenceFlow, error)` | route the body by format to the registered engine, run it, commit the named outputs (each a Ready datum, in sorted name order), return the outgoing flows. |
| `LoadData` / `UploadData` | bind declared inputs before, commit outputs after. |
| `ScriptFormat()` / `Script()` | the task's `scriptFormat` hint and script body. |
| `AddBoundaryEvent(be)` / `BoundaryEvents()` | attach / inspect boundary events. |
| `ActivityType()` / `TaskType()` | introspection. |
| `ForCompensation()` | whether the task is a compensation handler. |

Behavior worth knowing:

- **Routing by format.** `scriptFormat` is the seam — `Exec` dispatches the body
  to the engine claiming that MIME type. Claim conflicts are rejected loudly at
  engine construction, not at runtime; a routing miss fails the task loud.
- **Outputs by return.** The engine's returned `Outputs` map commits as named
  process data, each key its own Ready datum, in deterministic sorted order.
- **Fail-loud, not silent.** A missing engine (`"##None"`), a routing miss, or a
  script exception fails the task through the ordinary fault machinery — a
  matching error boundary event can interrupt it.
- **The sandbox** is the engine's contract, not the task's — `adapters/lua`, for
  instance, loads only base/table/string/math and binds execution to the task
  context so a hung script aborts on cancellation or timeout.

Prefer a Script Task when the step is portable data logic you want to keep as
editable script; when it needs your own Go types or I/O, use a
[Service Task](service-task.md) instead.

## See also

- Examples: `examples/script-task/`
- Related guides: [Service Task](service-task.md) · [Business Rule Task](business-rule-task.md) · [Custom script engine](../extending/script-engine.md) · [Scope & the data plane](../concepts/scope-and-data.md)
- Design: [ADR-031 — The Script Task and the pluggable Script Engine seam](../../design/ADR-031-script-task-and-script-engine-seam.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/activities` · `go doc github.com/dr-dobermann/gobpm/pkg/script`
