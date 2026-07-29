---
title: The value model
description: Value and its four kinds — scalar, Collection, Record, Map — plus the three tiers that back a structural value.
---

# The value model

Everything a process reads or writes is a `data.Value`: a locked, cloneable
box around a Go value with one string type name. A plain scalar implements only
`Value`; a value becomes **structural** by also implementing one of three
optional capabilities — `Collection` (a list), `Record` (named fields), or
`Map` (data-keyed entries). A structural value is navigable by path
(`order.items[0].price`), and its *kind* is discovered by type assertion, never
declared. This page is the developer reference for the `Value` interface, the
four kinds, the concrete `values.*` types you construct, and the three tiers
that can back a record.

## The four kinds

A value's kind is the capability interface it implements — a value implements
**at most one** structural capability (ADR-011 v.6 §2.9.1 / v.7 §2.9.7):

| Kind | Interface | Package | What it is | Path step |
|---|---|---|---|---|
| scalar | *(none — `Value` only)* | `pkg/model/data` | a leaf: string, int, bool, a wrapped struct treated whole | — (leaf) |
| list | `data.Collection` | `pkg/model/data` | ordered, homogeneous-by-construction elements, with a cursor | `[i]` |
| record | `data.Record` | `pkg/model/data` | string-keyed, heterogeneous, insertion-ordered fields (its keys are *schema*) | `.field` |
| map | `data.Map` | `pkg/model/data` | homogeneous values under NON-EMPTY string keys (its keys are *data*) | `["key"]` |

> Record vs Map is the keys question: a **record's** keys are its schema
> (a fixed field set), a **map's** keys are data (arbitrary run-time strings,
> so deletion is first-class). A scalar implements neither — it is a path leaf.

## Value — the common interface

Every value, scalar or structural, satisfies `data.Value`:

```go
type Value interface {
    Get(ctx context.Context) any        // copy of the value (collection: element at cursor)
    Update(context.Context, any) error  // set the value (collection: element at cursor)
    Lock()                              // guard an in-place update through a pointer
    Unlock()
    Type() string                       // string type name
    Clone() Value                       // deep copy (each instance gets its own)
}
```

`Get`/`Update` on a `Collection` act on the element at the current cursor and
**panic on an empty collection** — use `GetAt`/`SetAt` for cursor-free index
access. `Clone` is why an instance never shares mutable data with its snapshot
or its siblings.

## Typed extraction — `data.As`

When your code holds a bare `data.Value` (a reader result, a `Record` field, a
data association's value), extract the payload with `As` — the canonical typed
idiom — instead of a hand assertion on `Get`:

```go
// avoid — a mismatch silently yields the zero value
amount, _ := v.Get(ctx).(int)

// prefer — a mismatch is an ordinary, self-identifying error
amount, err := data.As[int](ctx, v)
if err != nil {
    return fmt.Errorf("reading order amount: %w", err)
}
```

```go
func As[T any](ctx context.Context, v Value) (T, error)
```

`As` rejects a nil `Value` and, on a type mismatch, returns an error naming
both the held and the requested type (`"As: value holds string, not int"`) —
including interface types (`data.As[fmt.Stringer]`). It asserts the payload
`Get` returns, so on a `Collection` that is the element at the cursor, on a
`Map` the whole `map[string]T` entry set, on a `Record` the `map[string]any`
of field payloads. When you already hold a *concrete* generic value
(`Variable[T]`, `Array[T]`, `Map[T]`), prefer its `T`-suffix accessors
(`GetT`, `GetAtT`, `EntryT`) — no assertion at all.

## Collection — the list capability

`Collection` extends `Value` with ordered access, an iteration cursor, and
index writes. The methods most code reaches for:

| Method | Role |
|---|---|
| `Count() int` | length. |
| `GetAt(ctx, index) (any, error)` | read element at index — cursor-free. |
| `SetAt(ctx, index, value) error` | write at index: `[0,len)` replaces, `len` appends, `>len` errors — cursor-free (ADR-011 v.6 §2.9.3). |
| `Add(ctx, value) error` | append to the end. |
| `GetAll(ctx) []any` | every element. |

The full cursor and mutation surface:

| Method | Role |
|---|---|
| `Rewind()` / `GoTo(position)` / `Next(dir)` | move the iteration cursor. |
| `Index() any` / `GetKeys() []any` | current index / all indices. |
| `Insert(ctx, value, index)` / `Delete(ctx, index)` | insert at / remove at index. |
| `Clear()` | drop all elements, reset the cursor. |

## Record — the named-fields capability

`Record` is the optional structural capability that makes a value navigable by
`.field`. Its keys are its schema:

```go
type Record interface {
    Value
    Keys() []string                                   // field names, insertion order
    Field(ctx context.Context, name string) (Value, error)  // ObjectNotFound if absent
    SetField(ctx context.Context, name string, v Value) error
}
```

`SetField`'s shape enforcement is the implementation's own: the dynamic
`values.Record` accepts a new field, while a reflection/codegen adapter over a
native struct rejects an unknown name (see [the three tiers](#the-three-tiers)).

## Map — the data-keyed capability

`Map` is the dictionary capability (ADR-011 v.7 §2.9.7): homogeneous values
under arbitrary run-time keys, navigable by `["key"]`, with **deterministic
sorted enumeration** over Go's randomized map iteration and first-class
deletion:

```go
type Map interface {
    Value
    Keys() []string                                   // ascending (sorted) order
    Entry(ctx context.Context, key string) (any, error)   // ObjectNotFound if absent
    SetEntry(ctx context.Context, key string, value any) error  // upsert; empty key is an error
    DeleteEntry(ctx context.Context, key string) error    // ObjectNotFound if absent
}
```

## Constructing values

The concrete implementations live in `pkg/model/data/values`; each is generic,
so `T` fixes the element/field/entry type (`[any]` is the zero-setup form for
engine-assembled data):

| Kind | Constructor | Panicking twin |
|---|---|---|
| scalar | `values.NewVariable[T](value T) *Variable[T]` | — |
| list | `values.NewArray[T](values ...T) *Array[T]` | — |
| record | `values.NewRecord(fields ...RecordField) (*Record, error)` | `values.MustRecord(...)` |
| map | `values.NewMap[T](entries map[string]T) (*Map[T], error)` | `values.MustMap[T](...)` |

A record field is a `values.RecordField{Name, V}`; the shorthand
`values.F(name, v)` builds one. Every concrete type also exposes **typed**
accessors alongside the `any` interface methods — `Variable.GetT() T`,
`Array.GetAtT(i) (T, error)`, `Map.EntryT(key) (T, error)`, and `GetP()/GetT()`
for pointer/value reads — so host code avoids `any` round-trips.

Here is the `structural-data` example building an order record —
`{ id, total, items: [ {sku, price}, … ] }`, a record of a scalar, a scalar,
and a list of records:

```go
item := func(sku string, price int) data.Value {
    return values.MustRecord(
        values.F("sku", values.NewVariable(sku)),
        values.F("price", values.NewVariable(price)))
}

order := values.MustRecord(
    values.F("id", values.NewVariable("A-1")),
    values.F("total", values.NewVariable(total)),
    values.F("items", values.NewArray[data.Value](
        item("widget", 50), item("gadget", 100))),
)
```

A `Value` becomes process data by wrapping it in a `data.ItemDefinition`
(`data.MustItemDefinition(order, …)`) and hanging it on a `data.Property` — see
[Item definitions & item-aware elements](item-definitions.md).

## Reading & writing by path

A structural value is addressed by a path — `.field` descends a record, `[i]`
a list, `["key"]` a map — resolved by the same seam that plain names use, so no
consumer needs a special API. The `structural-data` service task reads a nested
scalar straight through the narrow `DataReader`:

```go
d, err := r.GetData("order.items[0].price")
// …
fmt.Printf("  ▶ order.items[0].price = %v\n", d.Value().Get(ctx))
```

and the exclusive gateway routes on `order.total`. For writes,
`values.SetPath(ctx, root, path, v)` sets `v` at a path relative to `root`,
**auto-vivifying** missing intermediate records/lists on a permissive dynamic
target (a following `.field` creates a `values.Record`, a following `[i]` a
`values.Array`); it lives in `values`, not `data`, precisely because it
constructs those concrete types. An empty path is an error — a whole-value
write is `Value.Update`. Paths, `SchemaAt`, `Walk`, and `DiffValues` are
covered on [Reading & writing by path](structural.md).

## Run it

Running `examples/structural-data/` (`total = 150`, so the `> 100` branch
wins):

```
order.total = 150
  ▶ order.items[0].price = 50
  ▶ order.total > 100 → premium lane
✓ structural-data completed (Completed)
```

## The three tiers

The *kind* is what a value can do; the *tier* is how a record answers those
capabilities. All three are freely mixed and nested (a reflection-backed record
may contain a dynamic one), and the resolver only ever sees the capability
interfaces — it never knows which tier answers (ADR-011 v.6 §2.9.5):

| Tier | How you get it | When | Reflection |
|---|---|---|---|
| dynamic | `values.Record` / `values.Array` / `values.Map` | engine-assembled data, any depth, zero boilerplate | none |
| reflection adapter | `adapters.Wrap(&yourStruct)` | wrap your own Go struct as a live record, zero boilerplate | once per type, at first `Wrap`, off the execution path |
| codegen adapter | `go:generate` a static adapter (pre-empts the reflection builder) | per-type upgrade — a renamed field fails the build, not the run | none |

`adapters.Wrap(ptr any) (data.Value, error)` returns a **live** `data.Record`
view over your struct — wrap, not convert — so every data seam consumes it
through the ordinary capability interfaces. This is the one place the engine's
anti-reflection stance is deliberately relaxed, and the relaxation is bounded to
that once-per-type type walk. Native structs have their own page:
[Native Go structs](native-structs.md); registering a custom factory is
[Custom Value type](../extending/value-type.md).

## See also

- Examples: `examples/structural-data/`
- Related guides: [Item definitions & item-aware elements](item-definitions.md) · [Reading & writing by path](structural.md) · [Native Go structs](native-structs.md) · [Custom Value type](../extending/value-type.md)
- Design: [ADR-011 — process data flow](../../design/ADR-011-process-data-flow.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/data` · `go doc github.com/dr-dobermann/gobpm/pkg/model/data/values`
