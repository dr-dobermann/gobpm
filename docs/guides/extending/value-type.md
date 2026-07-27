---
title: Custom Value type
description: Register a builder so your Go type participates as a data.Value.
---

# Custom Value type

gobpm navigates process data through one interface — `data.Value` — with the
optional structural capabilities `Record` (fields) and `Collection` (elements)
layered on top. Your own Go types can join that data plane three ways: **tag**
the struct and `adapters.Wrap` it (reflection, once per type), **implement**
`data.Value` directly, or — for a type you *cannot* modify or tag (a
third-party struct, `time.Time`, a map type) — **register a builder** that
lifts it into a `Value`. This page is the third seam: `adapters.Register`.

> Reach for `Register` only when tagging is impossible. If you own the struct,
> `gobpm:"…"` tags plus `adapters.Wrap` are the zero-code path — see
> [Native Go structs](../data/native-structs.md).

## The seam

Registration installs a per-type **factory**: a function that receives a live
`*T` and returns the `data.Value` view the engine navigates.

```go
func Register[T any](build func(v *T) data.Value) error
```

| | |
|---|---|
| Package | `github.com/dr-dobermann/gobpm/pkg/model/data/adapters` |
| Type parameter | `T` — the concrete Go type you are lifting (never a pointer). |
| `build` | receives the live `*T`, returns the `data.Value` view. Must be non-nil. |
| Returns | an error (`build == nil` is rejected); never panics. |

`Register` pre-empts the reflection builder at two points: at `adapters.Wrap`
of a `T`, and at field classification when a `T` appears as a field or slice
element of another wrapped struct. Registration is **init-time by convention**;
a later `Register[T]` replaces the cache entry for *future* wraps only.

The value your factory returns is the ordinary `data.Value` contract:

```go
type Value interface {
    Get(ctx context.Context) any
    Update(context.Context, any) error
    Lock()
    Unlock()
    Type() string
    Clone() Value
}
```

To make the type **navigable** by path (`stamp.unix`, `feed.ticks[0].n`),
return a value that also satisfies `data.Record` (fields) or `data.Collection`
(elements). The simplest way is `values.MustRecord(values.F(name, v), …)` — the
engine-assembled dynamic record — so you never implement the interface by hand.

## Registering a builder

Call `Register[T]` once, at init time, before any process wraps a `T`:

```go
func init() {
    _ = adapters.Register(func(v *stamp) data.Value {
        return values.MustRecord(
            values.F("unix", values.NewVariable(v.at.Unix())))
    })
}
```

After this, any `adapters.Wrap(&stamp{…})` — or any wrapped struct with a
`stamp` field — surfaces `stamp` as a one-field record navigable at `.unix`,
without reflection touching it. The factory is the **Marshaler-analog**: it is
your `MarshalJSON` for the process data plane.

## A minimal real implementation

A type the host cannot tag is the canonical case — `time.Time`. Lift it into a
navigable record so a condition or mapping can reach `deadline.unix`:

```go
package main

import (
    "time"

    "github.com/dr-dobermann/gobpm/pkg/model/data"
    "github.com/dr-dobermann/gobpm/pkg/model/data/adapters"
    "github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

func init() {
    // time.Time is not ours to tag — a builder lifts it into a Record.
    _ = adapters.Register(func(t *time.Time) data.Value {
        return values.MustRecord(
            values.F("unix", values.NewVariable(t.Unix())),
            values.F("year", values.NewVariable(t.Year())))
    })
}
```

The `_ =` discards the error only because `build` is a non-nil literal; in
production code that registers dynamically, check it — a nil `build` is the one
rejected input.

## How the engine uses it

The factory runs the first time a `T` is wrapped; the resulting view is then
consumed exactly like any other `Value` — path walks, `SetPath`, `DiffValues`,
gateway conditions, and I/O mappings all go through the `Value` /
`Record` / `Collection` methods, with zero engine change. Reflection over a
registered type is skipped entirely: the factory *is* the adapter.

| You want to… | Do this |
|---|---|
| Wrap a struct **you own** | tag it + `adapters.Wrap` — no `Register`. |
| Wrap a type **you can't modify** | `adapters.Register[T]` a builder (this page). |
| Give a type its **own** `Value` behavior | implement `data.Value` directly — the passthrough kind, no adapter. |
| Make it navigable by path | return `values.MustRecord(…)` (or your own `data.Record`/`Collection`). |

> The registry is the one place gobpm's anti-reflection stance is deliberately
> relaxed, and it is bounded: a registered type never touches reflection at
> all, and a wrapped-by-tags type reflects **once** at first `Wrap`, then uses a
> cached-index accessor. Hot-path reflection stays rejected.

## Reference implementation

The **tag-driven adapter** is the built-in reference for this seam: the
`native-structs` example wraps the host's own `Order` struct with `gobpm:"…"`
tags and lets the reflection builder assemble the record — the path a
registered factory replaces. Read it for the full data path (wrap → live write
→ condition → commit-diff), then substitute `Register` where you can't tag.

Running `examples/native-structs/`:

```
  SetPath(order.total=150) → the LIVE struct: o.Total == 150
  quote → commit wrapped Receipt{Sum:5}
  reprice → commit wrapped Receipt{Sum:6}
  ▶ Value_Added receipt @quote
  ▶ Value_Updated receipt.sum @reprice
  ▶ order.total > 100 → premium lane
  ✓ completed (Completed)
```

The `order.total > 100` gateway condition reaches into the host's live struct
by path — the same navigability a `Register`-ed type gains.

## See also

- Examples: `examples/native-structs/`
- Related guides: [Native Go structs](../data/native-structs.md) · [The value model](../data/value-model.md) · [Reading & writing by path](../data/structural.md)
- Design: [ADR-011 — Process data flow](../../design/ADR-011-process-data-flow.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/data/adapters`
