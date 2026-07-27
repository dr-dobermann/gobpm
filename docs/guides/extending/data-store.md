---
title: Custom Data Store
description: Back a Data Store with your own storage.
---

# Custom Data Store

A **Data Store** is engine-global, item-aware data that outlives a Process
instance and is shared across instances within the running engine (BPMN
§10.4.1). The engine ships a non-durable in-memory adapter (`memstore`); to make
that data survive a restart — or share it across a cluster — you implement the
`datastore.DataStore` seam over your own backend (Redis, Postgres, an object
store) and register it on the engine. This page is the extension reference: the
interface you implement, the registration call, a minimal real adapter, and how
the engine drives it.

## The DataStore contract

A store is a named bag of `data.Data` addressed by an opaque key. You implement
four methods:

```go
type DataStore interface {
    // Get returns the datum stored under name; the bool is false when none
    // exists.
    Get(ctx context.Context, name string) (data.Data, bool, error)

    // Put stores (or replaces) d under name.
    Put(ctx context.Context, name string, d data.Data) error

    // Capacity reports the store's nominal item capacity (BPMN §10.4.1). It is
    // advisory in the in-memory adapter (ADR-030 §2.6) — a durable adapter may
    // enforce it.
    Capacity() int

    // IsUnlimited reports whether the store has no capacity bound.
    IsUnlimited() bool
}
```

| Member | You implement | Contract |
|---|---|---|
| `Get(ctx, name)` | the read path | return `(datum, true, nil)` on a hit; `(nil, false, nil)` on a miss — a miss is **not** an error. |
| `Put(ctx, name, d)` | the write path | store or replace `d` under `name`. |
| `Capacity()` | the nominal item bound | advisory in `memstore`; a durable adapter *may* enforce it by rejecting a `Put` past the bound. |
| `IsUnlimited()` | whether a bound exists | `true` when the store has no capacity limit. |

> The value you store and return is a `data.Data` — the same item-aware type a
> `Property`, `Parameter`, or `DataObject` carries (build one with
> `data.NewPathData` or hand back what you received). Your adapter persists and
> restores that item, not a raw scalar.

Two properties the engine relies on: a **miss is a clean `false`**, never an
error (an unknown key is a normal query, not a fault); and a store is **its own
namespace** — distinct `dataStoreRef`s never share state, so one adapter
instance backs exactly one ref.

## The reference implementation

The built-in `memstore` adapter is the canonical implementation — read it before
writing your own:

| Type | Role |
|---|---|
| `memstore.Store` | a concurrency-safe, in-memory `datastore.DataStore` (`memstore.New(opts…)`). |
| `memstore.WithCapacity(n)` | set the nominal capacity (advisory; `n <= 0` leaves it `Unlimited`). |
| `memstore.Registry` | the default `datastore.Registry` — a set of named stores (`NewRegistry`, `Register`, `Store`). |

`memstore.New()` returns an unbounded store; your adapter mirrors its shape —
`Get`/`Put`/`Capacity`/`IsUnlimited` over whatever backend you choose.

## Registration

Register each store on the engine with the `thresher.WithDataStore` option — the
same seam the built-in `memstore` uses, so your adapter drops straight in:

```go
func WithDataStore(ref string, store datastore.DataStore) Option
```

| Parameter | Meaning |
|---|---|
| `ref` | the `dataStoreRef` a `DataStoreReference` reads and writes (its BPMN key). |
| `store` | any `datastore.DataStore` — the in-memory `memstore`, or your durable adapter. |

Call it **once per distinct store**; registering an already-used `ref` replaces
it. There is no `SetGenerator`/`adapters.Register`-style global — a store is
per-engine, wired at construction:

```go
eng, err := thresher.New("orders-engine",
    thresher.WithDataStore("shared", myredis.New(client, "gobpm:")))
```

## A minimal adapter

The whole seam is four methods. A read-through wrapper that logs every access,
delegating storage to any inner `datastore.DataStore`:

```go
type auditStore struct {
    inner datastore.DataStore
    log   *slog.Logger
}

func (a *auditStore) Get(
    ctx context.Context, name string,
) (data.Data, bool, error) {
    d, ok, err := a.inner.Get(ctx, name)
    a.log.Info("datastore get", "name", name, "hit", ok)
    return d, ok, err
}

func (a *auditStore) Put(ctx context.Context, name string, d data.Data) error {
    a.log.Info("datastore put", "name", name)
    return a.inner.Put(ctx, name, d)
}

func (a *auditStore) Capacity() int    { return a.inner.Capacity() }
func (a *auditStore) IsUnlimited() bool { return a.inner.IsUnlimited() }
```

Register it like any store:

```go
store := &auditStore{inner: memstore.New(), log: slog.Default()}
eng, _ := thresher.New("audited", thresher.WithDataStore("shared", store))
```

## How the engine uses it

A `DataStoreReference` in a process names a store by `dataStoreRef`; the engine
resolves it against the registered stores and calls your `Put`/`Get` as the
reference is written or read:

- a **`DataOutputAssociation`** (Node → DataStore) drives `Put` — the task's
  output lands in your store under the reference's key;
- a **`DataInputAssociation`** (DataStore → Node) drives `Get` — the store's
  value flows into a later task's input, in a *different* instance.

Because the store lives on the engine, not the instance, the value crosses
instance boundaries. Running `examples/data-store/` — a writer instance stores
`42`, a separate reader instance reads it back through the same `"shared"` store:

```
✓ writer instance stored 42 in DataStore "shared" key "counter"
✓ reader instance read 42 from the shared DataStore
✓ data-store demo: the value outlived the writer instance and crossed into the reader through the engine-global store
```

**Reach for a custom Data Store when** the built-in `memstore` isn't enough:
you need the shared data to **survive an engine restart** (a durable backend),
to be **shared across a cluster** of engine processes, or to be governed by an
external system of record. If the data is per-instance and scope-resident, you
want a [Data Object](../data/data-objects.md), not a Data Store; if it's
engine-global but ephemeral is fine, the default `memstore` already covers you —
see [Data Store](../data/data-store.md).

## See also

- Examples: `examples/data-store/`
- Related guides: [Data Store](../data/data-store.md) · [Data Objects](../data/data-objects.md) · [Custom repository](repository.md)
- Design: [ADR-030 — Data Objects and Store](../../design/ADR-030-data-objects-and-store.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/datastore`
