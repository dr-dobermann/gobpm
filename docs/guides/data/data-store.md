---
title: Data Store
description: Engine-global storage shared across instances.
---

# Data Store

A **Data Store** is item-aware storage that outlives any single process
instance and is shared across every instance in the running engine. Reach for it
when a value must survive the instance that produced it — a counter, a cache, a
hand-off between two otherwise unrelated processes. There are two halves: the
engine-side **store** (`datastore.DataStore`, registered on the `Thresher`) and
the flow-side **reference** (`data_stores.DataStoreReference`, an
`ItemAwareElement` a task associates with, just like a Data Object). This page
is the data-model reference — the interfaces, the reference type, and the real
construction/read/write calls.

## Taxonomy

| | |
|---|---|
| BPMN category | Data → **Data Store** / **DataStoreReference** (§10.4.1) |
| Store port | `github.com/dr-dobermann/gobpm/pkg/datastore` — `DataStore`, `Registry` |
| Default adapter | `github.com/dr-dobermann/gobpm/pkg/datastore/memstore` — `Store`, `Registry` |
| Flow reference | `github.com/dr-dobermann/gobpm/pkg/model/data_stores` — `DataStoreReference` |
| Embeds | `data.ItemAwareElement`, `flow.BaseElement` |
| Registered via | `thresher.WithDataStore(ref, store)` |
| Contrast | a [Data Object](data-objects.md) is per-instance, scope-resident, and dies with the instance |

## The store port

The engine holds each store behind the `datastore.DataStore` interface —
item-aware data addressed by an opaque name. The default adapter is the
in-memory `memstore`; a durable adapter is a swap-in behind this same interface.

```go
type DataStore interface {
    Get(ctx context.Context, name string) (data.Data, bool, error)
    Put(ctx context.Context, name string, d data.Data) error
    Capacity() int
    IsUnlimited() bool
}
```

| Member | Role |
|---|---|
| `Get(ctx, name)` | fetch the datum under `name`; the `bool` is `false` when none exists. |
| `Put(ctx, name, d)` | store (or replace) `d` under `name`. |
| `Capacity()` | nominal item capacity (§10.4.1) — advisory in `memstore`, a durable adapter may enforce it. |
| `IsUnlimited()` | whether the store has no capacity bound. |

A `Registry` resolves a store by its reference id. An unregistered ref is an
error — fail-loud, since a reference to an unknown store is a configuration
mistake, not a silent auto-provision:

```go
type Registry interface {
    Store(ref string) (DataStore, error)
}
```

> You rarely call `Get`/`Put`/`Store` yourself — the engine reroutes a task's
> data associations to the named store for you. You implement this interface
> only to supply a custom backing (see [Custom Data Store](../extending/data-store.md)).

## The default adapter (memstore)

`memstore` is the non-durable, concurrency-safe in-memory store:

| Symbol | Signature | Role |
|---|---|---|
| `memstore.New` | `New(opts ...Option) *Store` | build an in-memory store. |
| `memstore.WithCapacity` | `WithCapacity(n int) Option` | set a nominal (advisory) capacity. |
| `memstore.Unlimited` | `const Unlimited = 0` | the default: no capacity bound. |
| `memstore.NewRegistry` | `NewRegistry() *Registry` | a registry of named stores, registered up front. |

`memstore.Store` implements the full `datastore.DataStore` interface
(`Get`/`Put`/`Capacity`/`IsUnlimited`); its capacity is advisory — a `Put` past
a nominal capacity is not rejected.

## The flow reference

A task never names a store directly; it associates with a `DataStoreReference`,
a flow-scope `ItemAwareElement` that carries a `dataStoreRef`. Data flowing
into/out of the reference flows into/out of the engine store named by that ref,
keyed by the reference's `Name()`.

```go
func New(
    name, dataStoreRef string,
    idef *data.ItemDefinition,
    state *data.SrcState,
    baseOpts ...options.Option,
) (*DataStoreReference, error)
```

| Parameter | Meaning |
|---|---|
| `name` | the reference name — also the **key** the value is stored under in the engine store. |
| `dataStoreRef` | the store's registration id; two references with the same `dataStoreRef` point at one backing store. |
| `idef` | the item definition (the value's shape). |
| `state` | the initial `data.SrcState` (e.g. `data.ReadyDataState`). |
| `baseOpts` | base-element options (id, docs). |

The reference wires to a node through one of two association methods — the only
difference between a writer and a reader:

| Method | Direction | Effect |
|---|---|---|
| `AssociateSource(n, sourceIDs, transformation)` | Node → DataStore | binds node `n`'s output (by source id) **into** the store — a `DataOutputAssociation`. |
| `AssociateTarget(n, transformation)` | DataStore → Node | binds the store **into** node `n`'s input — a `DataInputAssociation`. |

Both take an optional `data.FormalExpression` transformation (`nil` for a
straight copy). Introspection: `DataStoreRef()`, `Name()`, `ID()`, `EType()`.

## Build it

Register one in-memory store on the engine under a ref (here `"shared"`); call
`WithDataStore` once per distinct store. Registering an already-used ref
replaces the previous store.

```go
eng, err := thresher.New("data-store-demo",
    thresher.WithDataStore("shared", memstore.New()))
```

The **writer** task carries an output parameter; a reference to `"shared"`,
named `"counter"`, binds that output into the store via `AssociateSource`:

```go
ref, err := datastores.New("counter", "shared", idef(), data.ReadyDataState)
if err != nil {
    return nil, err
}
if err := ref.AssociateSource(writer, []string{itemID}, nil); err != nil {
    return nil, err
}
```

The **reader** task, in its own process, names the *same* `"shared"` store and
binds it to its input via `AssociateTarget`. Built the same way — only the
association direction differs:

```go
ref, err := datastores.New("counter", "shared", idef(), data.ReadyDataState)
if err != nil {
    return nil, err
}
if err := ref.AssociateTarget(reader, nil); err != nil {
    return nil, err
}
```

Inside the reader's Go function the value arrives by id, like any other input:

```go
d, err := r.GetDataByID(itemID)
if err != nil {
    return nil, err
}
if v, ok := d.Value().Get(ctx).(int); ok {
    seen <- v
}
```

## Run it

```bash
cd examples/data-store && go run .
```

The writer instance completes, then a *separate* reader instance — launched
after the writer is done — reads back what the writer left in the store:

```
✓ writer instance stored 42 in DataStore "shared" key "counter"
✓ reader instance read 42 from the shared DataStore
✓ data-store demo: the value outlived the writer instance and crossed into the reader through the engine-global store
```

## Runtime behavior

- **Cross-instance, cross-process.** The reader runs in a *different* instance
  than the writer — from a different process — yet sees `42`. The value crossed
  the instance boundary through the engine-global store. That is the whole
  point; a Data Object cannot do this.
- **Name is the key.** The reference's `Name()` (`"counter"`) is the store key;
  the `dataStoreRef` (`"shared"`) selects the backing store. Distinct
  `dataStoreRef`s never share state.
- **Fail-loud resolution.** A `DataStoreReference` whose `dataStoreRef` was never
  registered on the engine is a configuration error, not a silent no-op — the
  registry rejects the unknown ref.
- **Not per-instance.** A `DataStoreReference` is *not* a `DataObject`. If you
  only need data for one instance's lifetime, use a [Data Object](data-objects.md)
  or a process property — they are scoped and die with the instance.
- **Swappable backing.** `memstore.New()` is in-memory and non-durable; a
  durable adapter behind the same `datastore.DataStore` interface swaps in under
  the same ref without touching process code — see [Custom Data Store](../extending/data-store.md).

## See also

- Example: [`examples/data-store/`](../../../examples/data-store/)
- Related guides: [Data Objects](data-objects.md) · [Item definitions & item-aware elements](item-definitions.md) · [The value model](value-model.md)
- Extend it: [Custom Data Store](../extending/data-store.md)
- Design: [ADR-030 — Data Objects and Store](../../design/ADR-030-data-objects-and-store.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/datastore` · `go doc github.com/dr-dobermann/gobpm/pkg/model/data_stores`
