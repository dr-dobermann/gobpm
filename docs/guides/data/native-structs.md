---
title: Native Go structs
description: Wrap your own Go types as live process data.
---

# Native Go structs

When your host application already owns its domain types — an `Order`, a
`Receipt` — you don't translate them into a parallel process model. `adapters.Wrap`
returns a **live** `data.Value` view over your struct that satisfies `data.Record`:
the engine reads it, writes it, walks it by path, and diffs it — every access
landing on the real Go field, with no copy to keep in sync. This is the one place
the engine's anti-reflection stance is deliberately relaxed, and the relaxation is
bounded — reflection walks a type once, at its first `Wrap`, off the execution path.

## Taxonomy

| | |
|---|---|
| BPMN category | data value adapter (ADR-011 v.6 §2.9.5) — an engine extension, not a BPMN element |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/data/adapters` |
| Produces | a `data.Value` that also satisfies `data.Record` — a live view over a `*struct` |
| Consumed by | every data seam unchanged: path walks, `values.SetPath`, `DiffValues`, conditions, mappings |
| The work | wrap a host `*struct` so it participates as process data — *wrap, not convert* |

Where it sits: this completes the structural-data quartet — see
[Reading & writing by path](structural.md) for the record/list/map seam it plugs into.

## Construction

Three functions build or register an adapter. `Wrap` is the workhorse:

```go
func Wrap(ptr any) (data.Value, error)
func MustWrap(ptr any) data.Value
func Register[T any](build func(v *T) data.Value) error
```

| Function | Meaning |
|---|---|
| `Wrap(ptr)` | wrap a live `*struct` as a navigable `data.Value` (satisfying `data.Record`). Returns a classified error on a nil, non-pointer, pointer-to-non-struct (unregistered), or nil-pointer argument. |
| `MustWrap(ptr)` | the panic-on-error twin of `Wrap` (the `values.MustRecord` idiom) — for a type you control. |
| `Register[T](build)` | install a custom adapter factory for `T`, pre-empting the reflection builder — the `Marshaler`-analog seam for types you can't tag. |

> `MustWrap` panics on a type it can't adapt (for example a non-struct). Use
> `Wrap` and check the error when the pointer comes from outside your control.

## How a struct maps to process data

The `gobpm:"..."` struct tag reconciles Go naming with process naming, and it is
the whole contract:

| Tag form | Effect |
|---|---|
| `gobpm:"total"` | expose field `Total` under the process path segment `total`. |
| `gobpm:"-"` | hide the field from the process entirely — invisible to conditions, mappings, and the commit-diff. |
| nested struct / `[]T` | surfaces as a live sub-record / list — `order.items.0.price` resolves into the slice element. |

Tag your host types so the engine knows the process-facing names:

```go
type Order struct {
    ID     string `gobpm:"id"`
    Total  int    `gobpm:"total"`
    Items  []Item `gobpm:"items"`
    Secret string `gobpm:"-"` // never visible to the process
}

type Item struct {
    SKU   string `gobpm:"sku"`
    Price int    `gobpm:"price"`
}
```

## The Record contract

The value `Wrap` returns satisfies `data.Record` — the optional structural
capability of a `data.Value`. That's what makes it navigable by `.field` path steps:

```go
type Record interface {
    Value

    // Keys lists the field names in insertion order.
    Keys() []string

    // Field returns the named field's value, or a classified
    // errs.ObjectNotFound error when the field is absent.
    Field(ctx context.Context, name string) (Value, error)

    // SetField sets (adds or replaces) the named field.
    SetField(ctx context.Context, name string, v Value) error
}
```

You never implement `Record` yourself for a native struct — the adapter does it.
A typed adapter rejects unknown field names on `SetField` (its shape is fixed by
the Go type), unlike the permissive dynamic `values.Record`.

## Build it

Wrap a live instance and hand it to the process as a property. The wrapped value
goes wherever a `data.Value` is expected:

```go
order := &Order{ID: "A-1", Total: 90,
    Items:  []Item{{SKU: "widget", Price: 50}},
    Secret: "host-only"}

wrapped := adapters.MustWrap(order)

proc, err := process.New("native-structs",
    data.WithProperties(
        data.MustProperty("order",
            data.MustItemDefinition(wrapped, foundation.WithID("order")),
            data.ReadyDataState)))
```

Because the view is live, a host-side structural write lands on the real struct:

```go
values.SetPath(context.Background(), wrapped,
    "total", values.NewVariable(150))
// order.Total is now 150 — the write went through the view.
```

A gateway condition reaches into the same struct by path — no engine change,
just the ordinary `data.Source` seam:

```go
d, err := ds.Find(ctx, "order.total")
if err != nil {
    return nil, err
}
total, _ := d.Value().Get(ctx).(int)
return values.NewVariable(total > 100), nil
```

A task commits a wrapped host type as its output, and the commit-diff treats it
as an ordinary record:

```go
return data.MustItemDefinition(
    adapters.MustWrap(&Receipt{Sum: sum}),
    foundation.WithID("receipt")), nil
```

## Run it

```bash
cd examples/native-structs && go run .
```

The host write lands on the live struct, the tasks commit wrapped receipts, and
the commit-diff reports a `DataChange` fact per changed path:

```
  SetPath(order.total=150) → the LIVE struct: o.Total == 150
  quote → commit wrapped Receipt{Sum:5}
  reprice → commit wrapped Receipt{Sum:6}
  ▶ Value_Added receipt @quote
  ▶ Value_Updated receipt.sum @reprice
  ▶ order.total > 100 → premium lane
  ✓ completed (Completed)
```

## Behavior worth knowing

| Aspect | What happens |
|---|---|
| Wrap, not convert | `Wrap(&order)` returns a `Record` view backed by the pointer; reads/writes by path go through it into the real fields — no copy to reconcile. |
| Reflection is once per type | the first `Wrap` of a type reflects its layout and caches a per-type accessor (the `encoding/json` type-cache pattern); every later access is a cached-index lookup, not a fresh reflect call — hot-path reflection stays out. |
| Committing wrapped outputs | returning a wrapped struct from a task is how its per-path changes reach the commit-diff and surface as `DataChange` facts (the `receipt` lines above). |
| Concurrency | after `Wrap`, access the value through the adapter (guarded by the root mutex). A host that mutates the struct directly, concurrently with process evaluation, owns that synchronization itself. |

## Variations

- **A type you can't tag.** For a third-party type you can't add `gobpm` tags to
  — or `time.Time`, a map type — register an adapter with `adapters.Register[T]`,
  the `Marshaler`-analog seam. Registration is init-time by convention; a later
  `Register` replaces the cache entry for future wraps only.
- **A type that is already a value.** A type that implements `data.Value` itself
  participates as-is — the passthrough kind, no wrapping needed.

## See also

- Example: [`native-structs`](../../../examples/native-structs/)
- Related guides: [Reading & writing by path](structural.md) · [Data overview](index.md) · [Expressions](expressions.md)
- Design: [ADR-011 — process data flow](../../design/ADR-011-process-data-flow.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/data/adapters`
