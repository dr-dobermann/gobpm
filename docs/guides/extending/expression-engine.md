---
title: Custom expression engine
description: Plug in an expression language.
---

# Custom expression engine

gobpm evaluates every BPMN `FormalExpression` — a flow condition, a timer
duration, a multi-instance cardinality, a correlation key, a computed
assignee — through one swappable seam: `expression.Engine`. The engine kind
(Go-native functors, the `lite` text language, or your own FEEL / JUEL / DSL)
is chosen **per expression, by its language URI**, so one process can mix
languages and each routes to the engine that claims it. This page is the
extension reference — the seam interface, the registration call, a minimal real
engine, and how the runtime uses it.

Reach for a custom engine when your models need an expression syntax gobpm
doesn't ship — a business-friendly FEEL dialect, a JUEL bridge for a migration,
or a domain DSL — without touching the elements that consume expressions.

## The Engine contract

An engine is anything that names its kind, enumerates the language URIs it
evaluates, and evaluates an expression against a data source:

```go
type Engine interface {
    // Type names the engine kind in the "##"-hint convention ("##GoExpr",
    // "##Lite", "##FEEL", ...) for the startup config.
    Type() string

    // Languages returns the expression-language URIs this engine
    // evaluates — never empty for a real engine, matched
    // case-insensitively, so the Registry can detect conflicts and print
    // the routing table.
    Languages() []string

    // Evaluate evaluates expr against src and returns the result.
    Evaluate(ctx context.Context, expr data.FormalExpression, src data.Source) (data.Value, error)
}
```

| Member | You implement it to… |
|---|---|
| `Type()` | return the `##`-kind string shown in the startup routing table (e.g. `"##FEEL"`). |
| `Languages()` | return every language URI you claim; a claim collision across engines fails engine construction loud. Never empty. |
| `Evaluate(ctx, expr, src)` | evaluate `expr` — a `data.FormalExpression` whose `Language()` matched one of your claims — against `src` (a `data.Source`: `Find(ctx, name)` resolves process data by name), returning a `data.Value`. |

> The signature mirrors `FormalExpression.Evaluate`, so the default is a thin
> pass-through and an adapter intercepts at exactly one point.

## Registering it

Register with the repeatable engine option — each call adds another engine, and
the claims fold into the routing registry at `thresher.New`:

```go
func WithExpressionEngine(e expression.Engine) Option
```

| Option | Effect |
|---|---|
| `thresher.WithExpressionEngine(e)` | register engine `e`. REPEATABLE — call once per engine; a duplicate language claim fails `New` loud. The batteries are prepended by default. |
| `thresher.WithoutDefaultExpressionEngines()` | start the registry EMPTY — no batteries, every engine explicit ("remove it from the runtime if unused"). |

The batteries registered out of the box:

| Engine | Kind | Language URI | Package |
|---|---|---|---|
| Go-native functors | `##GoExpr` | `gobpm:goexpr` | `pkg/model/expression/goexpr` |
| `lite` text language | `##Lite` | `gobpm:lite` | `pkg/model/expression/lite` |

An empty registry reports kind `##None` and fails any evaluation loud, listing
the registered claims — so a model whose expression language nobody claims never
silently no-ops.

## The reference implementation

The Go-native default, `goexpr`, is the smallest real engine — it claims the
functor language and delegates to each expression's own `Evaluate`. Your custom
engine follows the same shape, replacing the delegation with your evaluator:

```go
type Engine struct{}

func New() expression.Engine { return Engine{} }

func (Engine) Type() string { return "##GoExpr" }

func (Engine) Languages() []string {
    return []string{dgexpr.Language} // "gobpm:goexpr"
}

func (Engine) Evaluate(
    ctx context.Context, expr data.FormalExpression, src data.Source,
) (data.Value, error) {
    return expr.Evaluate(ctx, src)
}
```

Wiring your own engine beside the batteries is one repeatable option:

```go
engine, err := thresher.New("my-engine",
    thresher.WithExpressionEngine(myfeel.New()))
```

For a full text-language implementation (a real parser/evaluator over process
data, structural paths, builtins) study `lite` — it is stdlib-only and claims
`gobpm:lite`.

## How the runtime uses it

At `thresher.New`, every registered engine's `Languages()` fold into an
immutable `expression.Registry` — a `language → engine` map that is itself an
`Engine`. Every consumer (conditions, gateways, timers, multi-instance,
correlation, the user-task assignee binder) talks to that one `Registry`; on
each evaluation it routes by the expression's `Language()` to the engine that
claimed it. The registry is built before the engine runs and read concurrently
without locks.

Running `examples/expression-routing/` — one process, the two batteries, three
consumer sites — shows the routing live: a `lite` text condition and a `goexpr`
functor on the same task's outgoing flows, a `lite` `time()` branch on the
gateway, and a `lite`-computed user-task assignee:

```
  ▶ intake: checking the order
  ▶ fx-audit: rates["EUR"] < 1.2 (the ##GoExpr functor lane)
  ▶ urgent: the deadline is near (the lite time() branch)
✓ expression-routing completed: both engines routed their own languages,
  and the lite-computed assignee approved the urgent order
```

Each expression reached its own engine purely by its language URI — the same
routing your custom engine joins the moment you register it.

## See also

- Examples: `examples/expression-routing/`
- Related guides: [Expressions](../data/expressions.md) · [Custom rule engine](rule-engine.md) · [Custom script engine](script-engine.md) · [The engine (Thresher)](../concepts/engine.md)
- Design: [ADR-032 — Language-routed expression engines](../../design/ADR-032-language-routed-expression-engines.md) · [ADR-002 — Extension architecture](../../design/ADR-002-extension-architecture.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/expression`
