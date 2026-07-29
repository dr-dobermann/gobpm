---
title: Expressions
description: Conditions and computed values over process data.
---

# Expressions

An **expression** computes a value — a boolean or a string — from process data
at the moment the engine reaches it. You reach for one to gate *which* sequence
flow a gateway or task takes, or to *derive* a value such as a user task's
assignee. Every expression is a `data.FormalExpression` tagged with a
**language URI**; the engine routes each to whichever registered engine claims
that URI, so one process can mix text expressions and Go functors freely with
no extra wiring. Full program: [`examples/expression-routing/`](../../../examples/expression-routing/).

## Taxonomy

| | |
|---|---|
| BPMN category | `Expression` → **`FormalExpression`** (executable, §10.3.1) |
| Model interface | `github.com/dr-dobermann/gobpm/pkg/model/data` — `data.FormalExpression` |
| Routing layer | `github.com/dr-dobermann/gobpm/pkg/model/expression` — `Engine`, `Registry` |
| Text kind | `data.TextExpression` (a language URI + a body string) |
| Functor kind | `data/goexpr.GExpression` (a Go func evaluated in-process) |
| Batteries | `gobpm:lite` (text) and `gobpm:goexpr` (functor), registered by default |

Where it plugs into the runtime: conditions on flows, gateway branches, and any
consumer that accepts a `data.FormalExpression`.

## The FormalExpression contract

Every expression — text or functor — is a `data.FormalExpression`. Consumers
(`flow.WithCondition`, `activities.WithAssigneeExpr`, …) take this interface, so
they never care which engine backs it:

```go
type FormalExpression interface {
    foundation.Identifyer
    foundation.Documentator

    // Language returns the FormalExpression language in URI format.
    Language() string
    // Evaluate evaluates the expression and returns its result.
    Evaluate(ctx context.Context, source Source) (Value, error)
    // Result returns the evaluated result (error if not yet evaluated).
    Result() (Value, error)
    // ResultType returns the name of the result type.
    ResultType() string
    // IsEvaluated returns true if the result is ready.
    IsEvaluated() bool
}
```

`Language()` is the routing key. `data.Source` is the read side of process
data — a single `Find(ctx, name) (Data, error)` that both engines call through
the same structural-path resolver, so `order.customer.tier` and
`rates["EUR"]` resolve identically from text and from Go.

> **Note:** a `TextExpression` **refuses self-evaluation** — its own
> `Evaluate` returns an error. Text bodies are interpreted only through the
> engine registry, so evaluating one directly would silently bypass language
> routing. Functors (`goexpr`) evaluate themselves; the default `goexpr`
> engine just delegates to their `Evaluate`.

## Constructors

Most models need only the two battery constructors — a `lite` text condition
and, when Go is easier than a string, a `goexpr` functor:

| Constructor | Kind · language | Use it for |
|---|---|---|
| `lite.Cond(body)` | text · `gobpm:lite` | a `bool` condition on a flow (result type pre-declared `bool`). |
| `lite.Expr(body)` | text · `gobpm:lite` | a plain value expression (a string, a number). |
| `goexpr.Must(ds, res, fn)` | functor · `gobpm:goexpr` | a condition/value written in Go, reading data through `data.Source`. |

The full set — the raw text constructor and the two functor constructors:

| Constructor | Signature | Notes |
|---|---|---|
| `lite.Cond` | `Cond(body string, opts ...options.Option) (*data.TextExpression, error)` | `Expr` + a declared `bool` result. Malformed body → error at construction. |
| `lite.Expr` | `Expr(body string, opts ...options.Option) (*data.TextExpression, error)` | text expression pre-tagged `gobpm:lite`; result is whatever the body yields. |
| `data.NewTextExpression` | `NewTextExpression(language, body string, opts ...options.Option) (*TextExpression, error)` | the generic text constructor — any language URI (FEEL, JUEL, …); both args required. |
| `goexpr.New` | `New(ds data.Source, res *data.ItemDefinition, gfunc GExpFunc, opts ...options.Option) (*GExpression, error)` | functor expression; `res` sets the result type, `gfunc` is `func(ctx, ds) (data.Value, error)`. `ds` may be nil at construction. |
| `goexpr.Must` | `Must(ds data.Source, res *data.ItemDefinition, gfunc GExpFunc, opts ...options.Option) *GExpression` | panicking `New` — for package-level or example wiring. |

> `goexpr` here is `github.com/dr-dobermann/gobpm/pkg/model/data/goexpr` (the
> functor **model** element). It is a different package from
> `pkg/model/expression/goexpr`, which is the default **engine** that evaluates
> those functors — you import the former to build one, never the latter.

## Build it

A **condition** on a sequence flow gates whether that flow is taken. Mint it
with `lite.Cond` and attach it with `flow.WithCondition`:

```go
premiumCond, _ := lite.Cond(
    `order.total > 100 and order.customer.tier == "vip"`)

flow.Link(intake, xor, flow.WithCondition(premiumCond))
```

The *same* selection point can carry a Go functor beside the text condition —
one flow routed to `gobpm:lite`, the sibling to `gobpm:goexpr`. The functor
reads the rates map through the shared resolver (`ds.Find`):

```go
func eurRateOK() data.FormalExpression {
    return goexpr.Must(
        nil,                                              // Source bound at eval time
        data.MustItemDefinition(values.NewVariable(false)), // result type: bool
        func(ctx context.Context, ds data.Source) (data.Value, error) {
            d, err := ds.Find(ctx, `rates["EUR"]`)
            if err != nil {
                return nil, err
            }
            rate, _ := d.Value().Get(ctx).(float64)
            return values.NewVariable(rate < 1.2), nil
        })
}

flow.Link(intake, fxAudit, flow.WithCondition(eurRateOK()))
```

On an exclusive gateway the branches are conditions plus one **default flow**
(the else-branch, taken when no condition holds):

```go
urgentCond, _ := lite.Cond(`deadline < time("2026-12-31T00:00:00Z")`)
flow.Link(xor, urgent, flow.WithCondition(urgentCond))

df, _ := flow.Link(xor, standard)
xor.UpdateDefaultFlow(df)
```

An expression can also **compute a value**, not just a boolean. A `lite.Expr`
string expression derives a user task's assignee per instance —
`order.customer.tier + "-manager"` resolves to `"vip-manager"`:

```go
assignee, _ := lite.Expr(`order.customer.tier + "-manager"`)

activities.NewUserTask("approve",
    activities.WithAssigneeExpr(assignee),
    // ...
)
```

The data these expressions navigate are ordinary process properties — a nested
record, a map, a time — built the usual way:

```go
customer, _ := values.NewRecord(values.F("tier", values.NewVariable("vip")))
order, _ := values.NewRecord(
    values.F("total", values.NewVariable(500)),
    values.F("customer", customer))
rates, _ := values.NewMap(map[string]float64{"EUR": 1.09, "USD": 0.92})
```

> **Note:** the `lite.*` constructors return an `error` — a malformed body is
> rejected at *construction*, not at run time. Check it; the snippets above
> elide it for brevity.

## Run it

```bash
cd examples/expression-routing && go run .
```

The premium condition holds (total 500, tier `vip`), the deadline branch fires,
and the `goexpr` lane audits the EUR rate — all three sites in one run:

```
  ▶ intake: checking the order
  ▶ fx-audit: rates["EUR"] < 1.2 (the ##GoExpr functor lane)
  ▶ urgent: the deadline is near (the lite time() branch)
Approve the urgent order?
✓ expression-routing completed: both engines routed their own languages,
  and the lite-computed assignee approved the urgent order
```

## The `gobpm:lite` text language

`lite` is a small, stdlib-only text language over process data — no runtime
dependencies. It covers the common condition/value needs:

| Feature | Example |
|---|---|
| Numbers · strings · booleans · nil | `order.total > 100`, `status == "open"` |
| Times | `deadline < time("2026-12-31T00:00:00Z")` |
| Structural paths | `order.customer.tier`, `rates["EUR"]` |
| Short-circuit booleans | `a and b`, `a or b` |
| Builtins | `has(order.coupon)`, `len(items)`, `time(...)` |

`lite.Cond` pre-declares a `bool` result — what flow-gating requires;
`lite.Expr` yields whatever the body evaluates to. Use `Cond` on a flow, `Expr`
where a value is consumed.

## Routing & engine registry

The runtime consumers never talk to a concrete engine — they talk to a
`expression.Registry`, which is itself an `expression.Engine` and dispatches by
language:

| Symbol | Role |
|---|---|
| `expression.Engine` | `Type()`, `Languages() []string`, `Evaluate(ctx, expr, src)` — one engine's language claim + evaluation. |
| `expression.Registry` | folds every engine's claims into a `language → engine` map; immutable after `New`, read lock-free. |
| `expression.NewRegistry(engines ...Engine)` | rejects a nil engine, an empty claim, or a **duplicate** claim (two engines answering one language). |
| `expression.NoneType` (`"##None"`) | the empty registry's kind — every evaluation fails loud. |

Two engines ship in the batteries and are prepended by default: `goexpr`
(`gobpm:goexpr`) and `lite` (`gobpm:lite`). You extend or replace routing at
engine construction:

| Option (`pkg/thresher`) | Effect |
|---|---|
| `WithExpressionEngine(e expression.Engine)` | **repeatable** — register another engine (FEEL, JUEL, a custom DSL). A duplicate language claim fails construction loud. |
| `WithoutDefaultExpressionEngines()` | start the registry **empty** — no batteries; every engine registered by hand. An unclaimed language then fails loud, listing the claims that *are* registered. |

Building your own engine is its own page: [Custom expression engine](../extending/expression-engine.md).

## See also

- Example: [`examples/expression-routing/`](../../../examples/expression-routing/)
- Related guides: [Reading & writing by path](structural.md) · [Data overview](index.md) · [Exclusive gateway](../gateways/exclusive.md) · [Custom expression engine](../extending/expression-engine.md)
- Design: [ADR-032 — Language-routed expression engines](../../design/ADR-032-language-routed-expression-engines.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/expression` · `go doc github.com/dr-dobermann/gobpm/pkg/model/data`
