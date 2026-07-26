# Working with process data

How a gobpm process reads, writes, assembles, and observes **structured data**
(ADR-011 v.7 §2.9, landed by SRD-042…045 and SRD-047). This is the conceptual
map; the [examples](#worked-examples) are the runnable walk-throughs.

## The value model — one interface, four capabilities

Every process value implements `data.Value` (`Get`/`Update`/`Lock`/`Unlock`/
`Type`/`Clone`). Structure is expressed by **capabilities** on top of it:

| Capability | Meaning | Navigation |
|---|---|---|
| *(none)* | a scalar | whole-value only |
| `data.Collection` | an ordered list | `[i]` |
| `data.Record` | named fields, ordered | `.field` |
| `data.Map` | a data-keyed dictionary | `["key"]` |

A value's *kind* is which capability it implements (at most one); nesting
composes to any depth. Shape is discovered by **traversing the value**
(`data.SchemaAt`, `data.Walk`) — there is no stored schema artifact.

**Record vs map.** A `Record`'s keys are its *schema* — a fixed set of named
fields, in insertion order. A `Map`'s keys are *data* — arbitrary run-time
strings you grow key-by-key (`values.MustMap(map[string]T{…})`,
`SetEntry`/`DeleteEntry`), enumerated in **sorted** order (deterministic over
Go's randomized iteration), addressed `rates["EUR"]`. A native `map[string]V`
struct field participates live through `adapters.Wrap`; because a Go map value
is not addressable, native-map writes are **entry-level** (`SetEntry` replaces
a whole value) — a composite entry reads navigably but a deep write into it
errors loud rather than silently writing to a detached copy.

## The three tiers

| Tier | Construct | When |
|---|---|---|
| **dynamic** | `values.NewVariable(v)`, `values.NewArray(...)`, `values.MustRecord(values.F("name", v), …)` | engine-assembled data, any depth, zero setup |
| **native structs** | `adapters.MustWrap(&hostStruct)` | your own Go types participate **live** — wrap, not convert |
| **codegen** *(future)* | a `go:generate` adapter on the same seam | reflection-free per-type upgrade, by need |

Tiers mix freely: a wrapped struct nests inside a `values.Record`, and a
`data.Value`-typed struct field participates as itself (the *passthrough*
kind).

## Reading by path

One resolver serves **every** consumer — expressions, gateway conditions,
mappings, and in-process service code:

```go
// a gateway condition / expression / worker DataReader — the same grammar:
d, err := ds.Find(ctx, "order.items[0].price")
```

- `.field` descends into a record, `[i]` into a list, `["key"]` into a map
  (a bare number is a list index, a quoted string a map key: `[0]` vs `["0"]`);
- the head (`order`) resolves like any plain name — properties, task outputs;
- `SOURCE/addr` (the `/` provider split, e.g. `RUNTIME/STARTED_AT`) still runs
  **first**: `/` selects a provider, `.`/`[]` walk engine-managed values;
- a path into a scalar, a missing field, or an out-of-range index is a
  classified error naming the walked prefix.

## Writing and assembling

- **`values.SetPath(ctx, root, "items[0].price", v)`** sets a value at a path.
  On a **dynamic** target, missing intermediates auto-vivify (`.field` → a
  record, `["key"]` → a map, `[i]` → a list; an index appends only at `len` —
  no holes). A **typed** target (a wrapped struct) rejects unknown names and
  type clashes; a map upserts the key (keys are data).
- **`Collection.SetAt(ctx, i, v)`** is the atomic indexed write: `[0,len)`
  replaces, `==len` appends, past-`len` errors. It never moves the iteration
  cursor.
- **Output mapping assembles by head**: `WithOutputMapping` rules whose `Var`s
  share a head build **one** nested value —

  ```go
  activities.WithOutputMapping(
      tasks.OutputRule{Path: p1, Var: "order.total"},
      tasks.OutputRule{Path: p2, Var: "order.items[0].price"},
  ) // → ONE "order" record, not two flat variables
  ```

  A plain `Var` still emits its whole value unchanged.

## Observing changes

"Which data changed" is answered at **commit** (the activity boundary): the
scope diffs each committed value against its prior into `(path, ChangeType)`
entries, and each becomes a `DataChange` observability fact —

- a first commit → one `Value_Added` at the root (a new subtree is one change,
  not one per leaf);
- a nested re-commit → `Value_Updated` at the changed leaf;
- `DataChange` is **observer-only** (never echoed to the operator log — the
  flood guard): subscribe with `engine.Observe(myObserver)` and filter
  `f.Kind == observability.KindDataChange`; the path is
  `f.Details[observability.AttrDataPath]`.

Values embed **no** notification — change detection is the scope's job, so it
survives the engine's clone/commit execution model.

## Native structs

```go
type Order struct {
    ID     string `gobpm:"id"`     // rename: Go name → process name
    Total  int    `gobpm:"total"`
    Items  []Item `gobpm:"items"`  // a live Collection view
    Secret string `gobpm:"-"`      // hidden from the process entirely
}

v := adapters.MustWrap(&order)     // a LIVE data.Record view
```

- **Wrap, not convert** — the engine navigates your object; a `SetPath` writes
  into the live struct.
- **Bounded reflection**: the type is walked **once**, at the first `Wrap`,
  and cached (the `encoding/json` pattern); field access afterwards is a
  cached-index accessor. This is the engine's one deliberate reflection
  allowance (SAD-001 §6).
- **Per-tier contracts**: `Get` returns the **native Go object**; `Update`
  accepts the same struct (replace) or a `map[string]any` (merge); `Clone` is
  value-copy + fresh slices (pointer fields stay shared — documented).
- **Types you can't modify** plug in via the Marshaler-analog hook:

  ```go
  adapters.Register(func(v *time.Time) data.Value {
      return values.MustRecord(values.F("unix", values.NewVariable(v.Unix())))
  })
  ```

  A registered factory pre-empts the reflection builder at `Wrap` and at
  field classification; a type implementing `data.Value` itself needs neither.
- **Concurrency invariant**: after `Wrap`, access goes through the adapter
  (one root mutex per wrapped struct). A host mutating the struct directly,
  concurrently with process evaluation, owns that synchronization itself.

## Which tier when

| You have | Use |
|---|---|
| engine-assembled / ad-hoc data | dynamic `values.*` |
| your own Go types as process data | `adapters.Wrap` |
| a third-party type you can't modify | `adapters.Register` |
| a hot type needing reflection-free access | the codegen follow-up (rides `Register`) |

## Data Objects — named containers in scope

A **`DataObject`** (BPMN §10.4.1) is a diagram-visible, **named** container that
lives in the instance's scope tree — the same place a `Property` lives, resolved
by the same walk-up. Register one on the container that owns it:

```go
do, _ := dataobjects.New("order-total",
    data.MustItemDefinition(values.NewVariable(0), foundation.WithID("total")),
    data.ReadyDataState)
proc.Add(do)        // a Process-level object → seeded into the root scope
// sub.Add(do)      // a SubProcess-level object → seeded into the child scope
                    //   when it opens, disposed when it closes
```

Each instance gets its **own copy** (cloned from the snapshot), so concurrent
instances never share DataObject state. Read one **by name** — inside an
operation through the data reader, or from outside via the instance handle:

```go
d, _ := reader.GetData("order-total")   // service.DataReader, or handle.Data()
total := d.Value().Get(ctx)
```

A **`DataAssociation`** binds a DataObject to a task in either direction, and the
value always flows through the per-instance scope (never the shared association):

- **output** (`do.AssociateSource(task, []string{"<item-id>"}, nil)`) — the
  task's produced output is written into the DataObject (Node → DataObject);
- **input** (`do.AssociateTarget(task, nil)`) — the DataObject's value fills a
  task input (DataObject → Node).

`DataObjectReference` (the per-appearance alias) is a deliberate
non-implementation — see [SAD-001 §14.1](../design/SAD-001-vision-and-architecture.md)
for the collapse-to-DataObject translation rules.

## Data Stores — data that outlives the instance

Where a `DataObject` is per-instance, a **Data Store** (BPMN §10.4.1) is
**engine-global**: it outlives every instance and is shared across them. It is
modeled as an **infrastructure port** — an interface with an in-memory default,
registered on the engine like `Repository`/`MessageBroker` — so durability is
later a swappable adapter, not a reshape. Register one per `dataStoreRef`:

```go
eng, _ := thresher.New("engine",
    thresher.WithDataStore("orders", memstore.New()))  // call once per store
```

Registration is **fail-loud**: a `DataStoreReference` naming an unregistered
store is a configuration error, not a silent auto-provision.

A **`DataStoreReference`** is the flow-scope handle — it participates in
`DataAssociation`s **exactly like a `DataObject`**, so the task wiring is
identical, but its I/O routes to the engine-global store (the association carries
the `dataStoreRef`, and the reroute resolves the store from the engine registry,
keyed by the reference's name):

```go
ref, _ := datastores.New("counter", "orders",   // name, dataStoreRef
    data.MustItemDefinition(values.NewVariable(0), foundation.WithID("cnt")),
    data.ReadyDataState)
ref.AssociateSource(writerTask, []string{"cnt"}, nil) // Node → DataStore
ref.AssociateTarget(readerTask, nil)                  // DataStore → Node
```

A value one instance writes is read by a later instance through a reference to
the same store. `capacity` is advisory in the in-memory adapter (a write past it
is accepted); a durable adapter may enforce it.

## Worked examples

| Example | Shows |
|---|---|
| [`examples/structural-data/`](../../examples/structural-data/) | reading **into** a value by path; a gateway routing on `order.total` |
| [`examples/structural-output-mapping/`](../../examples/structural-output-mapping/) | **assembling** a nested value from a flat worker body |
| [`examples/data-change/`](../../examples/data-change/) | observing per-path `DataChange` facts |
| [`examples/native-structs/`](../../examples/native-structs/) | the host's own struct as live process data |
| [`examples/maps/`](../../examples/maps/) | the **map kind** — a dynamic and a native dictionary navigated by `["key"]`; per-entry map `DataChange` facts |
| [`examples/process-data/`](../../examples/process-data/) | a **DataObject** per branch: a task writes its result into the scope-resident object, read back by name from the instance handle |
| [`examples/data-store/`](../../examples/data-store/) | an engine-global **DataStore**: a writer instance stores a value a *separate* reader instance reads back through a `DataStoreReference` to the same store |

Design background: [ADR-011](../design/ADR-011-process-data-flow.md) (the
conception), [ADR-010](../design/ADR-010-process-data-model.md) (the data
plane it runs on), [ADR-030](../design/ADR-030-data-objects-and-store.md)
(the DataObject scope integration).
