---
title: Custom script engine
description: Plug in a scripting language.
---

# Custom script engine

A Script Task carries a script body and a `scriptFormat` MIME hint; the engine
routes the body to a **script engine** that interprets that format. gobpm ships
one — the pure-Go Lua interpreter in `adapters/lua` — but the seam is a plain
interface (`script.Engine`), so you can register a Starlark sibling, a custom
DSL, or a hosted-language runtime alongside it. Each engine owns the formats it
claims; the engine routes by the task's `scriptFormat`. This page is the
extension reference — the seam interface, the registration call, a minimal real
implementation, and how the engine uses it.

## The seam interface

An engine interprets one or more script formats and names its kind in the
`"##"`-convention:

```go
type Engine interface {
    // Type names the engine kind ("##Lua", ...) for the startup config
    // and the Script facts.
    Type() string

    // Formats returns the MIME hints this engine interprets — an
    // enumerable claim (never empty for a real engine), so the Registry
    // can detect conflicts and print the routing table.
    Formats() []string

    // Execute runs the script body against the read-only process-data
    // surface and returns the script's named outputs (nil or empty when
    // the script produces none). format is the task's scriptFormat — an
    // engine may interpret several dialects by it.
    Execute(
        ctx context.Context,
        format, script string,
        r service.DataReader,
    ) (Outputs, error)
}
```

| Member | You implement it to… |
|---|---|
| `Type()` | name the engine kind for startup config and Script facts — the `"##"`-hint (`"##Lua"`). |
| `Formats()` | enumerate the `scriptFormat` MIME hints you interpret — **never empty**, so the registry can build its routing table and reject conflicts. |
| `Execute(ctx, format, script, r)` | interpret `script` (routed by `format`) against the read-only data surface `r`, and return named `Outputs`. |

`Outputs` is a `map[string]data.Value` — each entry commits as its own Ready
datum (a script "sets variables"). The data surface `r` is `service.DataReader`
— the same narrow read-only interface a Service Task operation receives:

| `DataReader` method | Reads |
|---|---|
| `GetData(name)` | a datum by name (plain name = default scope; `"SOURCE/addr"` = a named source). |
| `GetDataByID(id)` | a datum by its `ItemDefinition` id. |
| `GetSources()` | the named data sources reachable through the reader. |
| `List(path)` | variable names at the default scope or a named source. |

> Your `Execute` gets a **read-only** surface — a script reads process data and
> returns outputs; it never mutates the data plane directly. The returned
> `Outputs` map is the only write channel, and the engine commits it.

## Registration

Register an engine with the repeatable `thresher.WithScriptEngine` option:

```go
func WithScriptEngine(e script.Engine) thresher.Option
```

The option is **repeatable** — each call registers another engine. At
`thresher.New`, the engines' format claims fold into a routing `Registry`; a
duplicate claim (two engines answering the same format) **fails construction
loudly**, so routing stays deterministic and operator-visible.

```go
engine, err := thresher.New("orders",
    thresher.WithScriptEngine(lua.New()),
    // thresher.WithScriptEngine(starlark.New()), // repeatable — coexist
)
```

The default is **no engines** — the empty `"##None"` registry, whose execution
fails with a wire-an-adapter error. A process with a Script Task must register
at least one engine that claims the task's `scriptFormat`.

## The built-in reference: `adapters/lua`

The batteries engine is `adapters/lua` — Lua via the pure-Go `gopher-lua` VM
(no cgo, static builds intact). It is the reference implementation; read it
before writing your own.

```go
package lua

func New() *Engine                                  // stateless — one Engine serves concurrent tracks
func (e *Engine) Type() string                      // "##Lua"
func (e *Engine) Formats() []string                 // the claimed scriptFormat MIME hints
func (e *Engine) Execute(ctx, format, body, r) (script.Outputs, error)
```

Every `Execute` builds its own sandboxed `LState` (base/table/string/math only;
`io`/`os` never loaded; the `load` family removed), context-bound so a hung
script aborts on cancellation. Scripts read process data lazily through a
read-only `data` global — an absent datum **raises** naming it (fail-loud, not
Lua's nil idiom; probe optional data with `has(name)`) — and produce outputs by
returning a table of named values (Lua numbers land as `float64`).

## A minimal implementation

The whole contract in one type — an engine that echoes its data into an output
(illustrative; a real engine parses and runs `script`):

```go
type EchoEngine struct{}

func (EchoEngine) Type() string      { return "##Echo" }
func (EchoEngine) Formats() []string { return []string{"text/x-echo"} }

func (EchoEngine) Execute(
    ctx context.Context, format, script string, r service.DataReader,
) (script.Outputs, error) {
    d, err := r.GetData("total")     // read process data through the surface
    if err != nil {
        return nil, err
    }
    return script.Outputs{           // named outputs commit as Ready data
        "echoed": d.Value(),
    }, nil
}
```

Register it exactly like the built-in: `thresher.WithScriptEngine(EchoEngine{})`.
Because the option is repeatable and each engine owns disjoint formats, your
`"##Echo"` and the built-in `"##Lua"` can serve the same process — the task's
`scriptFormat` picks the interpreter.

## How the engine uses it

The Script Task carries `(scriptFormat, body)` from its constructor:

```go
classify, _ := activities.NewScriptTask("classify", "text/x-lua", orderLua)
```

At construction, `thresher.New` folds every registered engine into an immutable
`script.Registry` — itself an `Engine`, so the task always talks to one
interface no matter how many interpreters are wired. When the task executes, the
registry looks up the engine claiming the task's `scriptFormat` and calls its
`Execute`; the returned `Outputs` commit as named process data the downstream
steps read.

`examples/script-task/` wires the Lua engine (`thresher.WithScriptEngine(lua.New())`)
and runs an embedded `order.lua` that classifies orders by discount tier.
Running it:

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

The `[script]` lines come from Lua's `print`; the `[report]` lines are a
downstream Service Task reading the `discount_pct` and `lane` outputs the script
committed.

## When to reach for it

Register a custom script engine when your process authors want to express step
logic **as data** — an embedded, edit-without-recompile script in a language
they already know — rather than as compiled Go. Reach for the built-in
`adapters/lua` first; write your own `script.Engine` only when you need a
different language or a differently-sandboxed runtime. For step logic that stays
in Go, a [Service Task](../tasks/service-task.md) operation is the direct path.

## See also

- Example: `examples/script-task/`
- Related guides: [Script Task](../tasks/script-task.md) · [Custom Operation](operation.md) · [Custom rule engine](rule-engine.md) · [Custom expression engine](expression-engine.md)
- Design: [ADR-031 — Script Task and Script Engine seam](../../design/ADR-031-script-task-and-script-engine-seam.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/script`
